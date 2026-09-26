import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from Backend.Server.controller_server import CrazyAppController, NodeInfo
from Backend.Server.job_system import Job
from Backend.Server.node_client import CrazyAppNode


def test_controller_records_dispatch_log_for_job():
    controller = CrazyAppController()
    controller.registry.nodes["node-01"] = NodeInfo(
        node_id="node-01",
        hostname="alpha",
        cpu_threads=8,
        ram_gb=16,
        os="windows",
        arch="x64",
        status="READY",
    )

    job = Job(
        job_id="JOB-4001",
        job_type="sum",
        input_data={"values": [10, 20, 30]},
        required_cpu_cores=2,
        required_ram_gb=4,
        priority=5,
        tasks=[
            {"id": "task-1", "type": "sum", "payload": [10, 20], "required_cpu_cores": 2, "required_ram_gb": 4},
            {"id": "task-2", "type": "sum", "payload": [30], "required_cpu_cores": 1, "required_ram_gb": 1},
        ],
    )

    result = controller.submit_job(job)

    assert result["status"] == "DISPATCHING"
    assert job.job_id in controller.job_history
    assert controller.job_history[job.job_id]["assignments"][0]["node_id"] == "node-01"


def test_node_handles_task_dispatch_payload():
    node = CrazyAppNode()
    payload = {
        "type": "TASK_DISPATCH",
        "task_id": "task-1",
        "payload": [1, 2, 3, 4],
        "node_id": node.node_id,
    }

    result = node.handle_dispatch(payload)

    assert result["task_id"] == "task-1"
    assert result["status"] == "COMPLETED"
    assert result["value"] == 10
