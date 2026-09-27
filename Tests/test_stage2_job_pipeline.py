import hashlib
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from Backend.Server.job_system import (
    DistributedExecutionManager,
    Job,
    NodeResource,
    TaskSpec,
    build_hash_benchmark,
)


def test_job_tracks_pipeline_and_retries_lost_tasks():
    nodes = [
        NodeResource(
            node_id="node-01",
            hostname="alpha",
            cpu_threads=8,
            ram_gb=16,
            cpu_available_percent=100,
            available_cpu_cores=8,
            available_ram_gb=16,
            status="READY",
        ),
        NodeResource(
            node_id="node-02",
            hostname="beta",
            cpu_threads=8,
            ram_gb=16,
            cpu_available_percent=100,
            available_cpu_cores=8,
            available_ram_gb=16,
            status="READY",
        ),
    ]
    nodes[0].status = "LOST"

    job = Job(
        job_id="JOB-5001",
        job_type="sum",
        input_data={"values": [10, 20, 30]},
        required_cpu_cores=1,
        required_ram_gb=1,
        priority=3,
        tasks=[
            {"id": "task-1", "type": "sum", "payload": [10, 20], "required_cpu_cores": 1, "required_ram_gb": 1},
            {"id": "task-2", "type": "sum", "payload": [30], "required_cpu_cores": 1, "required_ram_gb": 1},
        ],
    )

    result = DistributedExecutionManager().execute(job, nodes)

    assert result["status"] == "COMPLETED"
    assert job.status == "COMPLETED"
    assert any(task.status == "COMPLETED" for task in job.tasks)
    assert all(task.attempt >= 1 for task in job.tasks)
    assert result["total_result"] == 60


def test_hash_benchmark_uses_core_execution_and_reconstructs_output():
    benchmark = build_hash_benchmark("JOB-BENCH-1", chunks=[b"alpha", b"beta", b"gamma"])
    nodes = [
        NodeResource(
            node_id="node-11",
            hostname="alpha",
            cpu_threads=4,
            ram_gb=8,
            cpu_available_percent=100,
            available_cpu_cores=4,
            available_ram_gb=8,
            status="READY",
        ),
        NodeResource(
            node_id="node-12",
            hostname="beta",
            cpu_threads=4,
            ram_gb=8,
            cpu_available_percent=100,
            available_cpu_cores=4,
            available_ram_gb=8,
            status="READY",
        ),
    ]

    result = DistributedExecutionManager().execute(benchmark, nodes)

    expected = hashlib.sha256(b"".join([b"alpha", b"beta", b"gamma"]))
    assert result["status"] == "COMPLETED"
    assert result["final_result"] == expected.hexdigest()
    assert benchmark.status == "COMPLETED"
    assert len(benchmark.tasks) == 3
