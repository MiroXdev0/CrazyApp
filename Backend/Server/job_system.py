from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, Iterable, List, Optional

JOB_STATUSES = {"QUEUED", "SCHEDULED", "RUNNING", "COMPLETED", "FAILED", "CANCELLED"}
TASK_STATUSES = {"QUEUED", "SCHEDULED", "RUNNING", "COMPLETED", "FAILED", "LOST", "CANCELLED"}


def _utc_now() -> datetime:
    return datetime.now(timezone.utc)


def _normalize_status(value: str, valid: set[str], default: str) -> str:
    status = str(value or default).upper()
    if status == "CREATED":
        return default
    if status == "DISPATCHED":
        return "SCHEDULED"
    if status not in valid:
        return default
    return status


@dataclass
class TaskSpec:
    id: str
    type: str
    payload: Any = None
    required_cpu_cores: int = 1
    required_ram_gb: int = 1
    status: str = "QUEUED"
    attempt: int = 0
    node_id: Optional[str] = None
    output: Any = None
    started_at: Optional[datetime] = None
    completed_at: Optional[datetime] = None
    created_at: datetime = field(default_factory=_utc_now)
    error: Optional[str] = None

    def __post_init__(self) -> None:
        self.type = str(self.type)
        self.status = _normalize_status(self.status, TASK_STATUSES, "QUEUED")
        self.required_cpu_cores = max(1, int(self.required_cpu_cores))
        self.required_ram_gb = max(1, int(self.required_ram_gb))
        self.attempt = max(0, int(self.attempt))

    def mark_status(self, status: str) -> None:
        self.status = _normalize_status(status, TASK_STATUSES, self.status)
        if self.status == "RUNNING" and self.started_at is None:
            self.started_at = _utc_now()
        if self.status in {"COMPLETED", "FAILED", "LOST", "CANCELLED"} and self.completed_at is None:
            self.completed_at = _utc_now()

    def begin(self, node_id: Optional[str] = None) -> None:
        self.node_id = node_id or self.node_id
        self.attempt += 1
        if self.started_at is None:
            self.started_at = _utc_now()
        self.mark_status("RUNNING")

    def complete(self, output: Any = None) -> None:
        self.output = output
        self.mark_status("COMPLETED")

    def fail(self, reason: Optional[str] = None) -> None:
        self.error = reason
        self.mark_status("FAILED")

    def lost(self) -> None:
        self.mark_status("LOST")

    def reset_for_retry(self) -> None:
        self.error = None
        self.output = None
        self.node_id = None
        self.started_at = None
        self.completed_at = None
        self.mark_status("QUEUED")


@dataclass
class Job:
    job_id: str
    job_type: str
    input_data: Any
    required_cpu_cores: int
    required_ram_gb: int
    priority: int = 0
    tasks: List[TaskSpec] = field(default_factory=list)
    status: str = "QUEUED"
    created_at: datetime = field(default_factory=_utc_now)
    started_at: Optional[datetime] = None
    completed_at: Optional[datetime] = None
    deadline: Optional[datetime] = None

    def __post_init__(self) -> None:
        self.status = _normalize_status(self.status, JOB_STATUSES, "QUEUED")
        self.job_type = str(self.job_type)
        if self.required_cpu_cores <= 0:
            self.required_cpu_cores = 1
        if self.required_ram_gb <= 0:
            self.required_ram_gb = 1

        normalized: List[TaskSpec] = []
        if not self.tasks:
            self.tasks = [
                TaskSpec(
                    id=f"{self.job_id}-task-1",
                    type=self.job_type,
                    payload=self.input_data,
                    required_cpu_cores=self.required_cpu_cores,
                    required_ram_gb=self.required_ram_gb,
                    status="QUEUED",
                )
            ]

        for index, task in enumerate(self.tasks, start=1):
            if isinstance(task, TaskSpec):
                task.job_id = self.job_id
                normalized.append(task)
                continue
            if not isinstance(task, dict):
                raise TypeError(f"Unsupported task format for job {self.job_id}: {type(task)!r}")
            task_payload = task.get("payload", self.input_data)
            task_type = str(task.get("type", self.job_type))
            task_status = str(task.get("status", "QUEUED"))
            normalized.append(
                TaskSpec(
                    id=str(task.get("id", f"{self.job_id}-task-{index}")),
                    type=task_type,
                    payload=task_payload,
                    required_cpu_cores=int(task.get("required_cpu_cores", self.required_cpu_cores)),
                    required_ram_gb=int(task.get("required_ram_gb", self.required_ram_gb)),
                    status=task_status,
                    node_id=task.get("node_id"),
                    attempt=int(task.get("attempt", 0)),
                    output=task.get("output"),
                    error=task.get("error"),
                )
            )

        for task in normalized:
            task.type = task.type or self.job_type
        self.tasks = normalized

    @property
    def id(self) -> str:
        return self.job_id

    @property
    def task_count(self) -> int:
        return len(self.tasks)

    @property
    def completed_tasks(self) -> int:
        return sum(1 for task in self.tasks if task.status == "COMPLETED")

    @property
    def pending_tasks(self) -> int:
        return sum(1 for task in self.tasks if task.status not in {"COMPLETED", "FAILED", "CANCELLED"})

    def mark_status(self, status: str) -> None:
        self.status = _normalize_status(status, JOB_STATUSES, self.status)
        if self.status == "RUNNING" and self.started_at is None:
            self.started_at = _utc_now()
        if self.status in {"COMPLETED", "FAILED", "CANCELLED"} and self.completed_at is None:
            self.completed_at = _utc_now()

    def add_task(self, task: TaskSpec) -> None:
        task.type = task.type or self.job_type
        self.tasks.append(task)

    def is_finished(self) -> bool:
        return self.status in {"COMPLETED", "FAILED", "CANCELLED"}


@dataclass
class NodeResource:
    node_id: str
    hostname: str
    cpu_threads: int
    ram_gb: int
    cpu_available_percent: int = 100
    available_cpu_cores: int = 0
    available_ram_gb: int = 0
    status: str = "READY"
    connected: bool = True
    healthy: bool = True

    def __post_init__(self) -> None:
        if self.cpu_threads <= 0:
            self.cpu_threads = 1
        if self.ram_gb <= 0:
            self.ram_gb = 1
        if self.available_cpu_cores <= 0:
            estimated = int(self.cpu_threads * (self.cpu_available_percent / 100.0))
            self.available_cpu_cores = max(1, estimated)
        if self.available_ram_gb <= 0:
            self.available_ram_gb = max(1, self.ram_gb)

    @property
    def is_healthy(self) -> bool:
        return self.connected and self.healthy and self.status == "READY"

    def can_run(self, job: Job) -> bool:
        if not self.is_healthy:
            return False
        if self.available_cpu_cores < job.required_cpu_cores:
            return False
        if self.available_ram_gb < job.required_ram_gb:
            return False
        return True


class NodrenCore:
    @staticmethod
    def _coerce_bytes(value: Any) -> bytes:
        if isinstance(value, bytes):
            return value
        if isinstance(value, bytearray):
            return bytes(value)
        if isinstance(value, list):
            return b"".join(NodrenCore._coerce_bytes(item) for item in value)
        if isinstance(value, str):
            return value.encode("utf-8")
        if isinstance(value, (int, float)):
            return str(value).encode("utf-8")
        return str(value).encode("utf-8") if value is not None else b""

    @staticmethod
    def _compute_numeric_value(payload: Any) -> int:
        if isinstance(payload, list):
            return sum(NodrenCore._compute_numeric_value(item) for item in payload)
        if isinstance(payload, tuple):
            return sum(NodrenCore._compute_numeric_value(item) for item in payload)
        if isinstance(payload, dict):
            return NodrenCore._compute_numeric_value(payload.get("values", payload.get("data", [])))
        if isinstance(payload, (int, float)):
            return int(payload)
        if isinstance(payload, str):
            try:
                return int(payload)
            except ValueError:
                return 0
        return 0

    @staticmethod
    def execute_task(task: TaskSpec) -> Dict[str, Any]:
        task_type = str(task.type).lower()
        if "hash" in task_type or "sha" in task_type:
            payload = task.payload
            if isinstance(payload, list):
                chunks = [NodrenCore._coerce_bytes(item) for item in payload]
                digest = hashlib.sha256(b"".join(chunks)).hexdigest()
                task.output = digest
                task.status = "COMPLETED"
                return {"task_id": task.id, "status": "COMPLETED", "output": digest, "value": digest}
            digest = hashlib.sha256(NodrenCore._coerce_bytes(payload)).hexdigest()
            task.output = digest
            task.status = "COMPLETED"
            return {"task_id": task.id, "status": "COMPLETED", "output": digest, "value": digest}

        value = NodrenCore._compute_numeric_value(task.payload)
        task.output = value
        task.status = "COMPLETED"
        return {"task_id": task.id, "status": "COMPLETED", "output": value, "value": value}


class JobScheduler:
    def _pick_node(self, task: TaskSpec, nodes: Iterable[NodeResource]) -> Optional[NodeResource]:
        eligible = [
            node for node in nodes
            if node.is_healthy
            and node.available_cpu_cores >= max(task.required_cpu_cores, 1)
            and node.available_ram_gb >= max(task.required_ram_gb, 1)
        ]
        if not eligible:
            return None
        return max(eligible, key=lambda n: (n.available_cpu_cores, n.available_ram_gb, n.cpu_threads))

    def schedule(self, job: Job, nodes: Iterable[NodeResource]) -> Optional[Dict[str, Any]]:
        eligible = [node for node in nodes if node.can_run(job)]
        if not eligible:
            return None

        best = max(
            eligible,
            key=lambda n: (n.available_cpu_cores, n.available_ram_gb, n.cpu_threads),
        )

        return {
            "job_id": job.job_id,
            "node_id": best.node_id,
            "hostname": best.hostname,
            "status": "DISPATCHED",
            "task_count": job.task_count,
            "assigned_cpu_cores": min(job.required_cpu_cores, best.available_cpu_cores),
            "assigned_ram_gb": min(job.required_ram_gb, best.available_ram_gb),
        }

    def split_and_dispatch(self, job: Job, nodes: Iterable[NodeResource]) -> List[Dict[str, Any]]:
        assignments: List[Dict[str, Any]] = []
        node_list = list(nodes)

        for task in job.tasks:
            candidate = self._pick_node(task, node_list)
            if candidate is None:
                return assignments
            task.status = "SCHEDULED"
            task.node_id = candidate.node_id
            dispatch = {
                "job_id": job.job_id,
                "task_id": task.id,
                "node_id": candidate.node_id,
                "hostname": candidate.hostname,
                "status": "DISPATCHED",
                "required_cpu_cores": max(task.required_cpu_cores, job.required_cpu_cores),
                "required_ram_gb": max(task.required_ram_gb, job.required_ram_gb),
            }
            assignments.append(dispatch)

        return assignments


class DistributedExecutionManager:
    def __init__(self, max_retries: int = 3):
        self.max_retries = max_retries

    def _choose_node(self, task: TaskSpec, nodes: Iterable[NodeResource]) -> Optional[NodeResource]:
        eligible = [
            node for node in nodes
            if node.is_healthy
            and node.available_cpu_cores >= max(task.required_cpu_cores, 1)
            and node.available_ram_gb >= max(task.required_ram_gb, 1)
        ]
        if not eligible:
            return None
        return max(eligible, key=lambda n: (n.available_cpu_cores, n.available_ram_gb, n.cpu_threads))

    def _on_task_failure(self, task: TaskSpec, message: str) -> None:
        task.error = message
        if task.attempt >= self.max_retries:
            task.fail(message)
        else:
            task.lost()

    def _assemble_result(self, job: Job, task_results: List[Dict[str, Any]]) -> Any:
        if not task_results:
            return None
        task_type = str(job.job_type).lower()
        if "hash" in task_type or "sha" in task_type:
            joined = b"".join(NodrenCore._coerce_bytes(result.get("payload")) for result in task_results if result.get("payload") is not None)
            return hashlib.sha256(joined).hexdigest()
        values = [result.get("value", 0) for result in task_results]
        return sum(int(value) for value in values)

    def execute(
        self,
        job: Job,
        nodes: Iterable[NodeResource],
        queue_when_unavailable: bool = False,
    ) -> Dict[str, Any]:
        node_list = list(nodes)
        assignments: List[Dict[str, Any]] = []
        results: List[Dict[str, Any]] = []
        job.mark_status("QUEUED")

        if not node_list:
            job.mark_status("FAILED")
            return {"job_id": job.job_id, "status": "FAILED", "reason": "No nodes registered"}

        for task in job.tasks:
            attempts = 0
            while attempts < self.max_retries:
                node = self._choose_node(task, node_list)
                if node is None:
                    task.reset_for_retry()
                    if queue_when_unavailable:
                        job.mark_status("QUEUED")
                        return {
                            "job_id": job.job_id,
                            "status": "QUEUED",
                            "reason": f"No eligible node for task {task.id}",
                            "assignments": assignments,
                            "results": results,
                        }
                    job.mark_status("FAILED")
                    return {
                        "job_id": job.job_id,
                        "status": "FAILED",
                        "reason": f"No eligible node for task {task.id}",
                        "assignments": assignments,
                        "results": results,
                    }

                task.begin(node.node_id)
                if not node.is_healthy:
                    task.lost()
                    attempts += 1
                    continue

                execution = NodrenCore.execute_task(task)
                if execution["status"] == "COMPLETED":
                    assignment = {
                        "job_id": job.job_id,
                        "task_id": task.id,
                        "node_id": node.node_id,
                        "hostname": node.hostname,
                        "status": "COMPLETED",
                        "required_cpu_cores": max(task.required_cpu_cores, job.required_cpu_cores),
                        "required_ram_gb": max(task.required_ram_gb, job.required_ram_gb),
                        "value": execution["value"],
                    }
                    assignment_result = {
                        "task_id": task.id,
                        "node_id": node.node_id,
                        "value": execution["value"],
                        "status": "COMPLETED",
                        "payload": task.payload,
                    }
                    assignments.append(assignment)
                    results.append(assignment_result)
                    break

                task.lost()
                attempts += 1

            else:
                task.fail(f"Retry budget exhausted for {task.id}")
                job.mark_status("FAILED")
                return {
                    "job_id": job.job_id,
                    "status": "FAILED",
                    "reason": f"Retry budget exhausted for task {task.id}",
                    "assignments": assignments,
                    "results": results,
                }

        job.mark_status("COMPLETED")
        final_result = self._assemble_result(job, results)
        return {
            "job_id": job.job_id,
            "status": "COMPLETED",
            "assignments": assignments,
            "results": results,
            "total_result": final_result if not isinstance(final_result, str) else None,
            "final_result": final_result,
        }


def build_hash_benchmark(job_id: str = "JOB-BENCH-1", chunks: Optional[List[Any]] = None) -> Job:
    chunk_list = chunks or [b"alpha", b"beta", b"gamma", b"delta"]
    tasks = [
        {
            "id": f"{job_id}-task-{index}",
            "type": "sha256",
            "payload": chunk,
            "required_cpu_cores": 1,
            "required_ram_gb": 1,
        }
        for index, chunk in enumerate(chunk_list, start=1)
    ]
    return Job(
        job_id=job_id,
        job_type="sha256",
        input_data=chunk_list,
        required_cpu_cores=1,
        required_ram_gb=1,
        priority=10,
        tasks=tasks,
    )


__all__ = [
    "Job",
    "TaskSpec",
    "NodeResource",
    "JobScheduler",
    "DistributedExecutionManager",
    "NodrenCore",
    "build_hash_benchmark",
]
