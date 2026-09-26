import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from Backend.Server.job_system import DistributedExecutionManager, Job, NodeResource


def test_distributed_execution_dispatches_tasks_and_aggregates_result():
    manager = DistributedExecutionManager()
    nodes = [
        NodeResource(
            node_id="node-01",
            hostname="alpha",
            cpu_threads=16,
            ram_gb=32,
            cpu_available_percent=80,
            available_cpu_cores=8,
            available_ram_gb=16,
        ),
        NodeResource(
            node_id="node-02",
            hostname="beta",
            cpu_threads=8,
            ram_gb=16,
            cpu_available_percent=60,
            available_cpu_cores=4,
            available_ram_gb=10,
        ),
    ]

    job = Job(
        job_id="JOB-3001",
        job_type="sum",
        input_data={"values": [2, 3, 5, 7]},
        required_cpu_cores=2,
        required_ram_gb=4,
        priority=7,
        tasks=[
            {"id": "task-1", "type": "sum", "payload": [2, 3], "required_cpu_cores": 2, "required_ram_gb": 4},
            {"id": "task-2", "type": "sum", "payload": [5, 7], "required_cpu_cores": 1, "required_ram_gb": 2},
        ],
    )

    result = manager.execute(job, nodes)

    assert result["status"] == "COMPLETED"
    assert len(result["assignments"]) == 2
    assert result["total_result"] == 17
    assert result["results"][0]["value"] == 5
    assert result["results"][1]["value"] == 12


def test_distributed_execution_fails_when_no_node_has_capacity():
    manager = DistributedExecutionManager()
    nodes = [
        NodeResource(
            node_id="node-03",
            hostname="gamma",
            cpu_threads=4,
            ram_gb=8,
            cpu_available_percent=25,
            available_cpu_cores=1,
            available_ram_gb=2,
        )
    ]

    job = Job(
        job_id="JOB-3002",
        job_type="sum",
        input_data={"values": [1, 2, 3]},
        required_cpu_cores=4,
        required_ram_gb=8,
        priority=2,
        tasks=[
            {"id": "task-1", "type": "sum", "payload": [1, 2, 3], "required_cpu_cores": 4, "required_ram_gb": 8},
        ],
    )

    result = manager.execute(job, nodes)

    assert result["status"] == "FAILED"
    assert result["reason"] == "No eligible node for task task-1"
