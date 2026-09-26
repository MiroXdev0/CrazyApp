from fastapi import FastAPI

app = FastAPI(title="NEXUS Controller API")

@app.get("/status")
def status():
    return {
        "status": "online",
        "controller": "nexus-controller",
        "workers": 3,
        "jobs": 0,
    }

@app.get("/jobs")
def jobs():
    return [{"id": "JOB-1847", "status": "queued"}]
