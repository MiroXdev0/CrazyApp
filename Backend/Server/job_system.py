from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, Iterable, List, Optional


@dataclass
class TaskSpec:
    id: str
    type: str
    payload: Any
    required_cpu_cores: int = 1
    required_ram_gb: int = 1
    status: str = "CREATED"


@dataclass
class Job:
    job_id: str
    job_type: str
    input_data: Any
    required_cpu_cores: int
    required_ram_gb: int
    priority: int = 0
    tasks: List[TaskSpec] = field(default_factory=list)
    status: str = "CREATED"
    created_at: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    deadline: Optional[datetime] = None

    def __post_init__(self) -> None:
        if self.required_cpu_cores <= 0:
            self.required_cpu_cores = 1
        if self.required_ram_gb <= 0:
            self.required_ram_gb = 1

        if not self.tasks:
            self.tasks = [
                TaskSpec(
                    id=f"{self.job_id}-task-1",
                    type=self.job_type,
                    payload=self.input_data,
                    required_cpu_cores=self.required_cpu_cores,
                    required_ram_gb=self.required_ram_gb,
                    status="CREATED",
                )
            ]
        normalized: List[TaskSpec] = []
        for index, task in enumerate(self.tasks, start=1):
            if isinstance(task, TaskSpec):
                normalized.append(task)
                continue

            if not isinstance(task, dict):
                raise TypeError(f"Unsupported task format for job {self.job_id}: {type(task)!r}")

            normalized.append(
                TaskSpec(
                    id=str(task.get("id", f"{self.job_id}-task-{index}")),
                    type=str(task.get("type", self.job_type)),
                    payload=task.get("payload", self.input_data),
                    required_cpu_cores=int(task.get("required_cpu_cores", self.required_cpu_cores)),
                    required_ram_gb=int(task.get("required_ram_gb", self.required_ram_gb)),
                    status=str(task.get("status", "CREATED")),
                )
            )
        self.tasks = normalized

    @property
    def task_count(self) -> int:
        return len(self.tasks)

    def mark_status(self, status: str) -> None:
        self.status = status


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

    def can_run(self, job: Job) -> bool:
        if self.status != "READY":
            return False
        if self.available_cpu_cores < job.required_cpu_cores:
            return False
        if self.available_ram_gb < job.required_ram_gb:
            return False
        return True


class JobScheduler:
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
        remaining_nodes = list(nodes)

        for task in job.tasks:
            candidate = None
            for node in remaining_nodes:
                if node.status != "READY":
                    continue
                if node.available_cpu_cores < max(task.required_cpu_cores, job.required_cpu_cores):
                    continue
                if node.available_ram_gb < max(task.required_ram_gb, job.required_ram_gb):
                    continue
                candidate = node
                break

            if candidate is None:
                return assignments

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
    def _choose_node(self, task: TaskSpec, nodes: Iterable[NodeResource]) -> Optional[NodeResource]:
        eligible = [
            node for node in nodes
            if node.status == "READY"
            and node.available_cpu_cores >= max(task.required_cpu_cores, 1)
            and node.available_ram_gb >= max(task.required_ram_gb, 1)
        ]
        if not eligible:
            return None
        return max(eligible, key=lambda n: (n.available_cpu_cores, n.available_ram_gb))

    def _compute_task_value(self, payload: Any) -> int:
        if isinstance(payload, list):
            total = 0
            for value in payload:
                if isinstance(value, (int, float)):
                    total += int(value)
            return total
        if isinstance(payload, dict):
            values = payload.get("values", [])
            if isinstance(values, list):
                return self._compute_task_value(values)
        return 0

    def execute(self, job: Job, nodes: Iterable[NodeResource]) -> Dict[str, Any]:
        node_list = list(nodes)
        assignments: List[Dict[str, Any]] = []
        results: List[Dict[str, Any]] = []
        total_result = 0

        for task in job.tasks:
            node = self._choose_node(task, node_list)
            if node is None:
                job.mark_status("FAILED")
                return {
                    "job_id": job.job_id,
                    "status": "FAILED",
                    "reason": f"No eligible node for task {task.id}",
                }

            value = self._compute_task_value(task.payload)
            total_result += value
            assignment = {
                "job_id": job.job_id,
                "task_id": task.id,
                "node_id": node.node_id,
                "hostname": node.hostname,
                "status": "DISPATCHED",
                "required_cpu_cores": max(task.required_cpu_cores, job.required_cpu_cores),
                "required_ram_gb": max(task.required_ram_gb, job.required_ram_gb),
            }
            assignments.append(assignment)
            results.append({
                "task_id": task.id,
                "node_id": node.node_id,
                "value": value,
                "status": "COMPLETED",
            })

        job.mark_status("COMPLETED")
        return {
            "job_id": job.job_id,
            "status": "COMPLETED",
            "assignments": assignments,
            "results": results,
            "total_result": total_result,
        }
