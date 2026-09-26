from dataclasses import dataclass
from typing import List


@dataclass
class Worker:
    id: str
    cpu_cores: int
    ram_gb: int
    gpu: str
    arch: str
    online: bool = True


@dataclass
class Job:
    id: str
    command: str
    priority: int
    cpu_cores: int
    ram_gb: int
    gpu_required: bool = False


class Scheduler:
    def __init__(self, workers: List[Worker]):
        self.workers = workers

    def choose_worker(self, job: Job) -> str:
        eligible = [
            worker for worker in self.workers
            if worker.online and worker.cpu_cores >= job.cpu_cores and worker.ram_gb >= job.ram_gb
        ]

        if not eligible:
            raise RuntimeError("No eligible worker found")

        eligible.sort(key=lambda w: (w.cpu_cores - job.cpu_cores, -w.ram_gb), reverse=True)
        return eligible[0].id


if __name__ == "__main__":
    workers = [
        Worker("node-01", 12, 16, "RTX", "x64", True),
        Worker("node-02", 8, 32, "NVIDIA", "arm64", True),
    ]
    scheduler = Scheduler(workers)
    job = Job("JOB-1847", "simulation.exe", 5, 4, 8)
    print(scheduler.choose_worker(job))
