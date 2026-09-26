import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from Backend.Server.job_system import Job, JobScheduler, NodeResource


def test_job_scheduler_prefers_eligible_node_with_capacity():
    scheduler = JobScheduler()

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
            cpu_available_percent=35,
            available_cpu_cores=3,
            available_ram_gb=5,
        ),
    ]

    job = Job(
        job_id="JOB-2001",
        job_type="sum",
        input_data={"values": [1, 2, 3, 4, 5]},
        required_cpu_cores=4,
        required_ram_gb=8,
        priority=5,
        tasks=[{"id": "task-1", "type": "sum", "payload": [1, 2, 3]}],
    )

    assignment = scheduler.schedule(job, nodes)

    assert assignment is not None
    assert assignment["node_id"] == "node-01"
    assert assignment["task_count"] == 1
    assert assignment["status"] == "DISPATCHED"


def test_job_scheduler_rejects_ineligible_node():
    scheduler = JobScheduler()
    nodes = [
        NodeResource(
            node_id="node-03",
            hostname="gamma",
            cpu_threads=4,
            ram_gb=8,
            cpu_available_percent=40,
            available_cpu_cores=1,
            available_ram_gb=2,
        )
    ]

    job = Job(
        job_id="JOB-2002",
        job_type="heavy",
        input_data={"values": [9, 9, 9]},
        required_cpu_cores=4,
        required_ram_gb=8,
        priority=2,
        tasks=[{"id": "task-1", "type": "heavy", "payload": [9, 9, 9]}],
    )

    assignment = scheduler.schedule(job, nodes)

    assert assignment is None
