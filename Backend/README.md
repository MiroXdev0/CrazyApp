# Nodren Backend

This backend replaces the previous collection of Go, Python, and stub Rust servers with a single coherent architecture:

```text
                 HTTP/JSON
Desktop/Web  ────────────────> Go Controller
                                  │
                                  │ persistent TCP
                                  │ binary protocol
                                  ▼
                              Rust Worker
                                  │
                                  ▼
                           local execution
```

## Components

### Go controller

`Backend/Controller-Go`

Responsibilities:

- HTTP management API
- node registry
- node health
- job queue
- resource-aware placement
- persistent node sessions
- binary framing
- task/result tracking
- retry/requeue after node loss

Run:

```bash
cd Backend/Controller-Go
go test ./...
go run .
```

Defaults:

- Node TCP: `:9000`
- HTTP API: `:8080`

Environment variables:

```text
NODREN_NODE_ADDR
NODREN_HTTP_ADDR
```

### Rust worker

`Backend/Worker-Rust`

Responsibilities:

- machine/resource discovery
- worker registration
- resource requirement validation
- persistent controller connection
- heartbeat
- task batch decoding
- local task execution
- result batching

Run:

```bash
cd Backend/Worker-Rust
cargo run -- --controller=127.0.0.1:9000
```

Useful options:

```text
--id=<node-id>
--controller=<host:port>
--ram-gb=<value>
--gpu-vendor=<vendor>
--gpu-model=<model>
--gpu-vram-gb=<value>
```

## Test job

Once the controller and worker are running:

```http
POST /v1/jobs
Content-Type: application/json

{
  "command": "sum",
  "priority": 50,
  "requirements": {
    "cpu_cores": 1,
    "ram_gb": 1,
    "gpu_required": false
  },
  "payload_base64": "AQIDBAU="
}
```

The worker executes the task and returns the result through `TASK_RESULT_BATCH`.

## Design rule

Go answers:

> Where should this job run?

Rust answers:

> Can this worker safely run this job, and how should the local execution be managed?

The native C/C++ compute layer can be attached behind the Rust worker later through the existing C ABI. The backend should not duplicate native computation in Go.
