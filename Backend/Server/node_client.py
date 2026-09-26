import asyncio
import json
import os
import socket
import time


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
        payload = packet.get("payload", [])
        if isinstance(payload, list):
            value = sum(int(item) for item in payload)
        elif isinstance(payload, dict):
            value = sum(int(item) for item in payload.get("values", []))
        else:
            value = 0

        return {
            "task_id": packet.get("task_id", "unknown-task"),
            "node_id": packet.get("node_id", self.node_id),
            "status": "COMPLETED",
            "value": value,
            "message": "task executed by node core",
        }

    async def run(self):
        reader, writer = await asyncio.open_connection(self.host, self.port)
        print(f"[node] connected as {self.node_id}")
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
