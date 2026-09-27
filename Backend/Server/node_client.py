import asyncio
import json
import os
import socket
import time

try:
    from .job_system import NodrenCore, TaskSpec
except ImportError:
    from job_system import NodrenCore, TaskSpec


class CrazyAppNode:
    def __init__(self, host: str = "127.0.0.1", port: int = 8080):
        self.host = host
        self.port = port
        self.node_id = f"NODE-{os.getpid()}"
        self.hostname = socket.gethostname()
        self.cpu_threads = 8
        self.ram_gb = 16
        self.os = os.name
        self.arch = "amd64"
        self.status = "READY"

    async def register(self, writer: asyncio.StreamWriter):
        payload = {
            "type": "REGISTER",
            "node_id": self.node_id,
            "hostname": self.hostname,
            "cpu_threads": self.cpu_threads,
            "ram_gb": self.ram_gb,
            "os": self.os,
            "arch": self.arch,
        }
        writer.write((json.dumps(payload) + "\n").encode())
        await writer.drain()

    async def heartbeat_loop(self, writer: asyncio.StreamWriter):
        while True:
            await asyncio.sleep(5)
            packet = {
                "type": "HEARTBEAT",
                "node_id": self.node_id,
            }
            writer.write((json.dumps(packet) + "\n").encode())
            await writer.drain()

    def handle_dispatch(self, packet: dict):
        task = TaskSpec(
            id=str(packet.get("task_id", "unknown-task")),
            type=str(packet.get("task_type", "sum")),
            payload=packet.get("payload", []),
            required_cpu_cores=int(packet.get("required_cpu_cores", 1)),
            required_ram_gb=int(packet.get("required_ram_gb", 1)),
            node_id=packet.get("node_id", self.node_id),
            status="RUNNING",
        )
        result = NodrenCore.execute_task(task)
        return {
            "task_id": task.id,
            "node_id": packet.get("node_id", self.node_id),
            "status": "COMPLETED",
            "value": result["value"],
            "output": result["output"],
            "message": "task executed by node core",
        }

    async def run(self):
        reader, writer = await asyncio.open_connection(self.host, self.port)
        print(f"[node] connected as {self.node_id} ({self.hostname})")
        await self.register(writer)

        heartbeat_task = asyncio.create_task(self.heartbeat_loop(writer))
        try:
            while True:
                msg = await reader.readline()
                if not msg:
                    break
                packet = json.loads(msg.decode().strip())
                print(f"[node] controller -> {packet}")
                if packet.get("type") == "REGISTER_ACK":
                    print(f"[node] registered ack from controller")
                if packet.get("type") == "HEARTBEAT_ACK":
                    print(f"[node] heartbeat ack from controller")
                if packet.get("type") == "TASK_DISPATCH":
                    result = self.handle_dispatch(packet)
                    response = {
                        "type": "TASK_RESULT",
                        "task_id": result["task_id"],
                        "node_id": self.node_id,
                        "status": result["status"],
                        "value": result["value"],
                        "output": result.get("output"),
                    }
                    writer.write((json.dumps(response) + "\n").encode())
                    await writer.drain()
                if packet.get("type") == "ERROR":
                    print(f"[node] error: {packet.get('error')}")
        finally:
            heartbeat_task.cancel()
            writer.close()
            await writer.wait_closed()


async def main():
    node = CrazyAppNode()
    await node.run()


if __name__ == "__main__":
    asyncio.run(main())
