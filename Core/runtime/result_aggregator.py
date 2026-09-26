from dataclasses import dataclass


@dataclass
class JobResult:
    job_id: str
    worker_id: str
    status: str
    output: str


class ResultAggregator:
    def __init__(self):
        self.results: list[JobResult] = []

    def add_result(self, result: JobResult) -> None:
        self.results.append(result)

    def complete_count(self) -> int:
        return len(self.results)


if __name__ == "__main__":
    aggregator = ResultAggregator()
    aggregator.add_result(JobResult("JOB-1", "node-01", "ok", "done"))
    print(aggregator.complete_count())
