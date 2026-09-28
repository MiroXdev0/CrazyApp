# Nodren Backend

For the pre-release deployment, `nodren.exe` is the main Controller process
and also exposes the operational CLI commands. `nodren-worker.exe` is the
standalone worker process. `nodren-ui.exe` is the Avalonia desktop frontend;
it uses the Controller HTTP API and does not own backend process management.
Build the release with `scripts/build-release.ps1` on Windows or
`scripts/build-release.sh` on Unix-like systems.

The Controller worker listener defaults to `:9000`; the HTTP API defaults to
`:8080`. Set `NODREN_NODE_ADDR` and `NODREN_HTTP_ADDR` to bind specific
interfaces. Workers connect with `--controller <host:port>` or
`--controller=<host:port>`, and may use `NODREN_CONTROLLER_ADDR` as a fallback.
The worker release directory must keep `nodren_core.dll` beside
`nodren-worker.exe` on Windows (or `libnodren_core.so` on Linux).

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

The desktop UI is in `Frontend/Desktop/App`. It has separate API client,
model, settings, and view-model layers, polls `/health`, `/v1/nodes`, and
`/v1/jobs`, and submits the existing workload commands. Set
`NODREN_CONTROLLER_URL` or use the Settings tab to select the HTTP endpoint.
`nodren-ui --self-test` performs a non-GUI health, worker, and `sum` smoke
test against the configured Controller.

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

The worker sends workload commands and opaque binary payloads to the native
dispatcher through the C ABI. Current native workloads are `sum`, `xor`, and
`dot_product`. The worker build requires the repository's GCC/G++ toolchain so
Cargo can build the native core library alongside the worker.

Run:

```bash
cd Backend/Worker-Rust
cargo run -- --controller=127.0.0.1:9000
```

Useful options:

```text
--id=<node-id>
--controller=<host:port>
--cpu-cores=<value>
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

## Resource-aware scheduling

Worker registration advertises the worker ID, CPU core count, RAM in GiB,
architecture, operating system, and optional GPU vendor/model/VRAM. CPU and
RAM are discovered from the host by default; `--cpu-cores` and `--ram-gb` are
available as explicit test/configuration overrides. The node API exposes these
values together with the current state and `assigned_jobs`.

Jobs carry `requirements.cpu_cores`, `requirements.ram_gb`, and
`requirements.gpu_required`. The controller considers connected workers in
`READY` state and `BUSY` workers that still have capacity. `LOST` and
`OFFLINE` workers are not assigned new work; `BUSY` means the worker has no
remaining schedulable capacity.

Workers now accept multiple tasks up to their advertised CPU/RAM capacity.
The controller reports allocated CPU and RAM in `allocated_cpu_cores` and
`allocated_ram_gb`; `BUSY` means no remaining schedulable capacity, not that a
single task is running. The Rust worker uses a bounded task queue, executor
threads derived from its CPU capacity, and a per-task resource gate before
entering native execution. The connection reader remains available while
tasks execute and results are written asynchronously.

Placement is deterministic best-fit: among eligible workers, the controller
minimizes CPU slack, then RAM slack, then uses the worker ID as a tie-breaker.
This keeps larger workers available for jobs that need them. Assignment is
recorded in both the job's `node_id` and the node's `assigned_jobs` field.

If workers are known but none can satisfy a job's resource requirements, the
job becomes `FAILED` with error code `resource_requirements`. If no worker has
registered yet, or a suitable worker is temporarily busy/lost, the job remains
`QUEUED` so a later registration or recovery can satisfy it. This is a simple
capacity filter, not production-grade resource accounting or GPU scheduling;
GPU matching only checks whether a worker advertises a GPU model.

### Adaptive workload distribution

Jobs default to `distribution_mode: "automatic"`. The Controller keeps the
workload-specific partitioner and reducer in a small workload registry; the
scheduler only sees opaque partition payloads, unit counts, resource
requirements, and partial numeric results. `sum` and `xor` partition byte
ranges, while `dot_product` partitions corresponding vector ranges. Inputs of
64 units or fewer stay as one task; larger inputs are split into a bounded
pool of chunks and assigned dynamically as workers release capacity.

The scheduling capacity estimate is deterministic and intentionally not a
benchmark: `capacity_score = CPU cores + RAM GiB / 4`. Effective capacity
multiplies that score by the smaller of the available CPU and RAM fractions.
GPU metadata is still enforced for requirements but does not inflate the
score because GPU execution is not implemented. Automatic assignment chooses
the worker with the lowest assigned-units/effective-capacity ratio, subject
to current CPU/RAM availability. This gives stronger workers more work while
allowing a worker that finishes early to receive another partition.

The job API exposes `distribution`, `partitions`, `node_ids`, and each
partition's state, attempt, unit count, worker, and partial result. If a
worker disconnects, only its unfinished partitions are marked `REQUEUED` and
remain eligible for assignment; completed partitions are never submitted
again. The system does not claim exactly-once execution.

Manual mode is optional and uses `distribution_mode: "manual"` with
`manual_allocations`, for example `{"worker-a": 60, "worker-b": 40}`. The
Controller rejects totals other than exactly 100 percent and still applies
resource and worker-state checks. The CLI remains automatic by default and
its existing syntax is unchanged.

`dot_product` uses a little-endian binary payload: a `u32` element count,
followed by two vectors of that many `i32` values. Unsupported commands and
malformed payloads return failed jobs with a machine-readable error category;
they do not terminate the worker connection.

## Design rule

Go answers:

> Where should this job run?

Rust answers:

> Can this worker safely run this job, and how should the local execution be managed?

The native C/C++ compute layer is invoked behind the Rust worker through the
existing C ABI. The backend does not duplicate native computation in Go.

## End-to-end test

Run the external integration test from the repository root:

```bash
python Tests/Integration/cluster_test.py
```

It builds the controller and worker, starts two workers with different
resources, verifies deterministic placement, exercises worker loss and
reconnect, and validates the existing workloads. Python is only test
orchestration; it is not part of the Nodren runtime.

## Connection recovery semantics

Workers reconnect with bounded backoff and re-register under the same node ID.
The controller keeps one logical node record, replaces stale sessions, marks
disconnected workers `LOST`, and requeues jobs that were still `RUNNING` on
that worker. If a connection fails during execution, the controller cannot
know whether the worker completed the task before the failure; that task may
therefore execute again after requeue. Nodren does not claim exactly-once
execution.
