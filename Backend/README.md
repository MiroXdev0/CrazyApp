# Nodren Backend

For the pre-release deployment, `nodren.exe` is the main Controller process
and also exposes the operational CLI commands. `nodren.exe-CLI` is the
separate Rust CLI. `nodren-worker.exe` is the standalone worker process. The
Avalonia desktop frontend remains a separately built development component.
Build the release with `scripts/build-release.ps1` on Windows or
`bash scripts/build-release.sh linux-x64` on Linux x86-64. Linux x64 support
is Beta / Unstable; Windows x64 remains the primary supported platform.

The Controller worker listener defaults to `:9000`; the HTTP API defaults to
`:8080`. Set `NODREN_NODE_ADDR` and `NODREN_HTTP_ADDR` to bind specific
interfaces. Workers connect with `--controller <host:port>` or
`--controller=<host:port>`, and may use `NODREN_CONTROLLER_ADDR` as a fallback.
The worker release directory must keep `nodren-core.dll` beside
`nodren-worker.exe` on Windows (or `libnodren_core.so` on Linux).

The Linux release is written to `release/LinuxX64/` and contains
`nodren`, `nodren-worker`, `nodren-ui`, and `libnodren_core.so`. On a Linux
x86-64 host, run `bash scripts/test-linux-release.sh` after building to verify
Controller startup, worker registration, adjacent native-core loading, the UI
API self-test, and a `sum` job. The normal Avalonia window is checked only
when `DISPLAY` or `WAYLAND_DISPLAY` is available.

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
- worker telemetry and scheduler performance history
- explainable placement and partition timing

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
NODREN_STATE_FILE
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
--gpu-count=<value>
```

When `nvidia-smi` is available, the worker also discovers NVIDIA GPU count,
model, memory, driver, CUDA capability, and compute capability. Explicit GPU
flags override the corresponding discovered values; a CPU-only worker remains
valid when discovery is unavailable.

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

The scheduling capacity estimate starts with `capacity_score = CPU cores + RAM
GiB / 4`, then incorporates controller-side allocations, the latest worker
CPU/memory telemetry, and a smoothed performance factor learned from completed
partitions. GPU metadata is still enforced for requirements but does not
inflate the score because GPU execution is not implemented. Automatic
assignment chooses the worker with the lowest assigned-units/effective-
capacity ratio, subject to current CPU/RAM availability. Each partition stores
the effective capacity, observed load, and distribution mode that explain its
assignment. Unavailable host measurements are represented explicitly and fall
back to advertised capacity.

Partition sizing is adaptive: the workload registry still owns partitioning
and reduction, while the controller uses the sum of currently eligible worker
capacity to choose a bounded target chunk size. As workers complete work,
observed throughput adjusts future placement and lets idle workers receive
additional partitions.

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

The scheduler microbenchmark can be run with:

```bash
cd Backend/Controller-Go
go test -bench BenchmarkChoosePartitionNode -benchmem ./...
```

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

## Control API and CLI

The Go binary is both the Controller entrypoint and the operational CLI. With
the Controller running, the supported control commands are:

```text
nodren status
nodren controller info
nodren workers list|info <id>|ping <id>|pause <id>|resume <id>|remove <id>
nodren workers stats [<id>]
nodren jobs list|info <id>|stats <id>|partitions <id>|run <workload> ...|cancel <id>|pause <id>|resume <id>
nodren distribution show [job-id]
nodren distribution auto <job-id>
nodren distribution set <job-id> worker=percent [worker=percent ...]
nodren config show
nodren doctor
nodren monitor                 # live SSE event stream (alias: events)
```

Worker pause prevents new scheduling while preserving the worker session;
remove disconnects the session and requeues unfinished work. Job cancellation
is authoritative in the Controller, so late results from a cancelled task are
ignored. Job pause/resume currently applies to queued jobs; running-job
suspension is rejected because protocol version 1 has no task-suspend message.

The same operations are available over HTTP under `/v1/nodes/{id}` and
`/v1/jobs/{id}`. Distribution updates use
`PUT /v1/jobs/{id}/distribution`, are restricted to queued jobs, and require
manual allocations to total exactly 100 percent.

## Recovery and state synchronization

The Controller enforces explicit job states (`QUEUED`, `RUNNING`, `PAUSED`,
`COMPLETED`, `FAILED`, and `CANCELLED`) and partition states. Terminal jobs and
completed partitions cannot be moved back into execution; task results are
accepted only while the task ID is still mapped to the active partition, so
duplicate and late results are ignored.

When `NODREN_STATE_FILE` is set (the release entrypoint defaults it to
`nodren-state.json`), the Controller atomically snapshots jobs, partitions,
payloads, results, worker metadata, timestamps, and progress. On restart,
persisted workers are marked `OFFLINE`, unfinished assignments are requeued,
and jobs wait for a fresh worker connection before scheduling resumes.

Clients can subscribe to `GET /v1/events` as an SSE stream. Events cover worker
connection/telemetry/state changes, job creation/progress/lifecycle changes,
and partition assignment/completion/requeue. Telemetry events are coalesced so
heartbeat frequency does not turn the SSE stream into a high-rate metrics
transport. `GET /v1/nodes/{id}/stats`, `GET /v1/jobs/{id}/stats`, and
`GET /v1/jobs/{id}/partitions` expose the same data for scripts and dashboards.
The Avalonia client uses this stream, reconnects with a small backoff when the
Controller is unavailable, and displays current worker load and scheduler
capacity.

## General tasks

The Controller also exposes a typed task API under `/v1/tasks`. Supported task
types are `PROCESS`, `SCRIPT`, `COMMAND`, and `NATIVE_WORKLOAD`; native tasks
continue to use the existing partitioning and C ABI path. Process tasks are
dispatched through the persistent binary protocol to the Rust worker, which
executes the OS process with structured arguments, environment variables,
stdin, timeout, cancellation, bounded stdout/stderr capture, exit-code
reporting, and retry attempts.

Examples:

```text
nodren run process /usr/bin/printf -- hello
nodren run script python script.py -- arg1 arg2
nodren run command cmd.exe -- /C echo hello
nodren tasks list
nodren tasks result TASK-000001
nodren tasks cancel TASK-000001
```

Multiple task requests can be submitted with `POST /v1/tasks/batch`; each task
is scheduled independently and retains the shared `batch_id`. Task targets
can require an OS, architecture, runtime, capability, preferred worker, or
allowed worker list. Workers advertise runtimes, execution types, and coarse
capabilities during registration.

Artifacts use `POST /v1/artifacts` for streamed binary upload and
`GET /v1/artifacts/{id}` for download. Uploaded bytes are checksum-verified
and stored separately from the main state JSON. `GET /v1/artifacts?sha256=...`
provides a content-addressed lookup, and the CLI uses it before uploading a
repeat file or package. Input artifact references are sent to workers in
bounded `ARTIFACT_BEGIN`/`CHUNK`/`END` frames and staged in the worker task
directory without loading the complete artifact into memory. Folder packages
carry `nodren.manifest.json`; Unix executable modes are restored on extraction.
The current trust model is a trusted cluster: the worker is not a sandbox and
executes approved task types with its OS account's permissions. Artifact
distribution is separate from computation distribution: arbitrary programs
run whole on one worker, while multi-worker strategies remain adapter-backed
and are rejected until a workload adapter is supplied.

## AI execution plans

The first AI adapter is `distributed-process`. It creates a capability-aware
worker group and can launch one real process or script task per rank through
the existing generalized task path. Each rank receives
`NODREN_AI_EXECUTION_ID`, `NODREN_AI_GROUP_ID`, `NODREN_AI_RANK`,
`NODREN_AI_WORLD_SIZE`, and leader metadata. Model and dataset artifacts reuse
the existing artifact system.

```text
nodren ai inspect ./model
nodren ai plan ./model --workers 2 --strategy distributed-process
nodren ai run ./model --workers 2 --strategy distributed-process
nodren ai status AI-000001
nodren ai cancel AI-000001
```

This adapter does not yet implement model/computation sharding, peer-network
transport, tensor or pipeline parallelism, checkpoint upload, or output
artifact collection. Model shard metadata distinguishes file shards from
computation shards; computation shards and enabled checkpointing return
explicit capability errors until an adapter implements their runtime semantics.
