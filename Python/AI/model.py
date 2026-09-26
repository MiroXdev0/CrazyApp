def suggest_worker(job_name: str) -> str:
    return f"Best worker for {job_name}: node-01"


if __name__ == "__main__":
    print(suggest_worker("simulation"))
