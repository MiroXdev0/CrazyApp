from fastapi import FastAPI

app = FastAPI(title="NEXUS API")

@app.get("/health")
def health():
    return {"status": "ok", "service": "nexus"}

@app.get("/nodes")
def nodes():
    return [{"id": "node-01", "status": "online"}]
