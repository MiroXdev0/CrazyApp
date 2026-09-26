from dataclasses import dataclass, field
from typing import List


@dataclass
class Job:
    id: str
    command: str
    priority: int = 0
    status: str = "queued"


@dataclass
class JobQueue:
    jobs: List[Job] = field(default_factory=list)

    def enqueue(self, job: Job) -> None:
        self.jobs.append(job)

    def next(self) -> Job | None:
        if not self.jobs:
            return None
        self.jobs.sort(key=lambda job: job.priority, reverse=True)
        return self.jobs.pop(0)

    def size(self) -> int:
        return len(self.jobs)


if __name__ == "__main__":
    queue = JobQueue()
    queue.enqueue(Job("JOB-1", "task-a", 2))
    queue.enqueue(Job("JOB-2", "task-b", 5))
    print(queue.next())
