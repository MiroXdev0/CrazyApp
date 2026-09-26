import time


def heartbeat(worker_id: str) -> dict:
    return {
        "worker_id": worker_id,
        "status": "online",
        "timestamp": time.time(),
    }


if __name__ == "__main__":
    print(heartbeat("node-01"))
