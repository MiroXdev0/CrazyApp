from fastapi import FastAPI

app = FastAPI(title="Nodren API")

@app.get("/health")
def health():
    return {"status": "ok", "service": "nodren"}

@app.get("/nodes")
def nodes():
    return [{"id": "node-01", "status": "online"}]
