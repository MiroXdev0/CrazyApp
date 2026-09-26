import asyncio
import json
import time
from dataclasses import dataclass, field
from typing import Dict, Optional

try:
    from .job_system import DistributedExecutionManager, Job, JobScheduler, NodeResource
except ImportError:
    from job_system import DistributedExecutionManager, Job, JobScheduler, NodeResource


@dataclass
class NodeInfo:
    node_id: str
    hostname: str
    cpu_threads: int
    ram_gb: int
    os: str
    arch: str
    status: str = "READY"
    last_heartbeat: float = field(default_factory=time.time)
    connected_at: float = field(default_factory=time.time)


class NodeRegistry:
    def __init__(self):
        self.nodes: Dict[str, NodeInfo] = {}

    def add(self, node: NodeInfo):
        self.nodes[node.node_id] = node

    def update_heartbeat(self, node_id: str):
        node = self.nodes.get(node_id)
        if node:
            node.last_heartbeat = time.time()
            node.status = "READY"

    def mark_lost(self, node_id: str):
        node = self.nodes.get(node_id)
        if node:
            node.status = "LOST"

    def remove(self, node_id: str):
        self.nodes.pop(node_id, None)

    def snapshot(self):
        return [
            {
                "node_id": n.node_id,
                "hostname": n.hostname,
                "cpu_threads": n.cpu_threads,
                "ram_gb": n.ram_gb,
                "os": n.os,
                "arch": n.arch,
                "status": n.status,
                "last_heartbeat": n.last_heartbeat,
            }
            for n in self.nodes.values()
        ]

    def online_count(self):
        return len(self.nodes)


class CrazyAppController:
    def __init__(self, host: str = "0.0.0.0", port: int = 8080, heartbeat_timeout: float = 20.0):
        self.host = host
        self.port = port
        self.registry = NodeRegistry()
        self.scheduler = JobScheduler()
        self.execution_manager = DistributedExecutionManager()
        self.heartbeat_timeout = heartbeat_timeout
        self.server = None
        self._clients: Dict[str, asyncio.StreamWriter] = {}
        self.jobs: Dict[str, Job] = {}
        self.job_history: Dict[str, Dict[str, object]] = {}

    def submit_job(self, job: Job):
        self.jobs[job.job_id] = job
        nodes = [
            NodeResource(
                node_id=node.node_id,
                hostname=node.hostname,
                cpu_threads=node.cpu_threads,
                ram_gb=node.ram_gb,
                cpu_available_percent=100,
                available_cpu_cores=max(1, node.cpu_threads),
                available_ram_gb=max(1, node.ram_gb),
                status=node.status,
            )
            for node in self.registry.nodes.values()
        ]

        assignment = self.scheduler.schedule(job, nodes)
        if assignment is None:
            job.mark_status("FAILED")
            result = {"job_id": job.job_id, "status": "FAILED", "reason": "No eligible node"}
            self.job_history[job.job_id] = {"status": "FAILED", "assignments": [], "execution": result}
            return result

        job.mark_status("DISPATCHING")
        execution = self.execution_manager.execute(job, nodes)
        if execution["status"] == "FAILED":
            self.job_history[job.job_id] = {"status": "FAILED", "assignments": [], "execution": execution}
            return execution

        dispatch_entry = {
            "job_id": job.job_id,
            "status": "DISPATCHING",
            "assignment": assignment,
            "execution": execution,
            "assignments": execution["assignments"],
        }
        self.job_history[job.job_id] = dispatch_entry
        return {
            "job_id": job.job_id,
            "status": "DISPATCHING",
            "assignment": assignment,
            "execution": execution,
        }

    def handle_job_submit(self, packet: dict) -> dict:
        job_data = packet.get("job") or packet
        task_specs = job_data.get("tasks", [])
        normalized_tasks = []
        for task in task_specs:
            normalized_tasks.append({
                "id": task.get("id"),
                "type": task.get("type", job_data.get("job_type", "sum")),
                "payload": task.get("payload", job_data.get("input_data", [])),
                "required_cpu_cores": int(task.get("required_cpu_cores", job_data.get("required_cpu_cores", 1))),
                "required_ram_gb": int(task.get("required_ram_gb", job_data.get("required_ram_gb", 1))),
            })

        job = Job(
            job_id=str(job_data.get("job_id", f"JOB-{int(time.time() * 1000)}")),
            job_type=str(job_data.get("job_type", "sum")),
            input_data=job_data.get("input_data", {}),
            required_cpu_cores=int(job_data.get("required_cpu_cores", 1)),
            required_ram_gb=int(job_data.get("required_ram_gb", 1)),
            priority=int(job_data.get("priority", 0)),
            tasks=normalized_tasks,
        )
        return self.submit_job(job)

    async def handle_client(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
        remote = writer.get_extra_info("peername")
        print(f"[controller] connection from {remote}")
        while True:
            try:
                line = await asyncio.wait_for(reader.readline(), timeout=self.heartbeat_timeout)
            except asyncio.TimeoutError:
                print("[controller] client timeout")
                writer.close()
                await writer.wait_closed()
                return

            if not line:
                print("[controller] client disconnected")
                writer.close()
                await writer.wait_closed()
                return

            message = line.decode("utf-8").strip()
            if not message:
                continue

            try:
                packet = json.loads(message)
            except json.JSONDecodeError:
                writer.write((json.dumps({"type": "ERROR", "error": "invalid_json"}) + "\n").encode())
                await writer.drain()
                continue

            msg_type = packet.get("type")

            if msg_type == "REGISTER":
                node = NodeInfo(
                    node_id=packet["node_id"],
                    hostname=packet.get("hostname", "unknown"),
                    cpu_threads=int(packet.get("cpu_threads", 0)),
                    ram_gb=int(packet.get("ram_gb", 0)),
                    os=packet.get("os", "unknown"),
                    arch=packet.get("arch", "unknown"),
                )
                self.registry.add(node)
                self._clients[node.node_id] = writer
                print(f"[+] {node.node_id} connected")
                writer.write((json.dumps({"type": "REGISTER_ACK", "node_id": node.node_id}) + "\n").encode())
                await writer.drain()

            elif msg_type == "HEARTBEAT":
                node_id = packet.get("node_id")
                if node_id:
                    self.registry.update_heartbeat(node_id)
                    writer.write((json.dumps({"type": "HEARTBEAT_ACK", "node_id": node_id}) + "\n").encode())
                    await writer.drain()

            elif msg_type == "NODE_STATUS":
                node_id = packet.get("node_id")
                if node_id:
                    self.registry.update_heartbeat(node_id)
                    writer.write((json.dumps({"type": "NODE_STATUS", "status": "ok", "online_count": self.registry.online_count()}) + "\n").encode())
                    await writer.drain()

            elif msg_type == "DISCONNECT":
                node_id = packet.get("node_id")
                if node_id:
                    self.registry.remove(node_id)
                    self._clients.pop(node_id, None)
                    print(f"[-] {node_id} disconnected")
                writer.close()
                await writer.wait_closed()
                return

            elif msg_type == "JOB_SUBMIT":
                result = self.handle_job_submit(packet)
                writer.write((json.dumps({"type": "JOB_DISPATCHED", "job": result}) + "\n").encode())
                await writer.drain()

            elif msg_type == "COMMAND_RESULT":
                print(f"[controller] result from {packet.get('node_id')}: {packet.get('result')}")
                writer.write((json.dumps({"type": "RESULT_ACK", "status": "ok"}) + "\n").encode())
                await writer.drain()

            else:
                writer.write((json.dumps({"type": "ERROR", "error": "unknown_message"}) + "\n").encode())
                await writer.drain()

    async def cleanup_lost_nodes(self):
        while True:
            await asyncio.sleep(5)
            now = time.time()
            for node_id, node in list(self.registry.nodes.items()):
                if now - node.last_heartbeat > self.heartbeat_timeout:
                    self.registry.mark_lost(node_id)
                    print(f"[-] {node_id} heartbeat timeout")

    async def start(self):
        self.server = await asyncio.start_server(self.handle_client, self.host, self.port)
        print(f"CrazyApp Controller started on {self.host}:{self.port}")
        asyncio.create_task(self.cleanup_lost_nodes())
        async with self.server:
            await self.server.serve_forever()


async def main():
    controller = CrazyAppController()
    await controller.start()


if __name__ == "__main__":
    asyncio.run(main())
