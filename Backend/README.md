# Nodren Backend

The backend is the Go Controller/control plane and the Rust Worker runtime.
The Controller exposes an HTTP/JSON management API and schedules work over a
persistent TCP connection using Nodren's binary protocol. The Worker validates
and executes assigned workloads, reports heartbeats/results, and reconnects
after connection loss. Native workloads pass from Rust through the C ABI to
the C/C++/x86-64 assembly Core.

```text
Rust CLI / Avalonia desktop app
              │ HTTP/JSON
              ▼
        Go Controller
              │ persistent TCP / binary protocol
              ▼
        Rust Worker
              │ C ABI
              ▼
  Native C/C++/assembly Core
```

## Windows release

The user-facing Windows x64 release contains exactly:

```text
Norden.exe
norden.exe
norden-worker.exe
norden-core.dll
```

`Norden.exe` is the Avalonia desktop application and manages the Go Controller
internally, including startup and shutdown. Users do not start a separate
Controller executable. `norden.exe` is the Rust CLI and can be invoked as
`norden` when the release directory is on `PATH`. `norden-worker.exe` is the
standalone Rust Worker; it loads the adjacent `norden-core.dll` through the
existing C ABI for native workloads.

Build and test the Windows release from the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\test-clean-release.ps1
```

The release build requires Go, Rust, .NET 10, Visual Studio C++ Build Tools,
and LLVM `clang`. The distinct `Norden.exe` and `norden.exe` names require
NTFS per-directory case sensitivity in the output directory. The build script
uses `%LOCALAPPDATA%\Nodren\release` when the workspace cannot enable it.

## Controller

Source: `Backend/Controller-Go`

Responsibilities include the HTTP API, worker registry, resource-aware
scheduling, jobs/tasks, artifact handling, partitioning, and recovery after
worker loss. The runtime state is persisted to a JSON file; configure its path
with `NODREN_STATE_FILE`. SQL assets elsewhere in the repository are not the
Controller's runtime state backend.

For backend development and tests:

```powershell
cd Backend/Controller-Go
go test ./...
go vet ./...
go run .
```

The development Controller defaults to worker TCP `:9000` and HTTP `:8080`.
`NODREN_NODE_ADDR` and `NODREN_HTTP_ADDR` configure these bind addresses.
Running the Controller directly is a development workflow; the Windows
end-user application starts and manages its own Controller.

## Worker

Source: `Backend/Worker-Rust`

The standalone Worker advertises host resources and capabilities, registers
with the Controller, sends periodic heartbeats, receives work, validates
resource requirements, stages declared artifacts, executes native or local
process/script workloads, and reports results. Its reconnect behavior and
persistent protocol session are independent of the desktop UI.

To build or test the Worker during development:

```powershell
cd Backend/Worker-Rust
cargo test
cargo build
```

On Windows, run these commands from a Visual Studio Developer PowerShell
session with LLVM `clang` available on `PATH`; the native build script invokes
the MSVC compiler and `clang-cl`/`clang` for the C/C++ and assembly sources.

The Windows release build uses Visual Studio C++ tools plus LLVM `clang` for
the native build. On supported Linux development hosts, the build script uses
GCC/G++ for the native Core. Worker options include `--controller
<host:port>`, `--id <worker-id>`, `--cpu-cores <count>`, `--ram-gb <count>`,
`--worker-concurrency <count>`, and optional GPU metadata. Run
`norden-worker.exe --help` for the current Windows executable's full usage.

## Scheduling and workloads

The Controller performs resource-aware job placement and tracks work,
partition, and worker state. Partitionable native `sum`, `xor`, and
`dot_product` workloads can be divided into bounded partitions; automatic
distribution schedules eligible partitions based on worker capacity and
observed throughput, while manual mode accepts worker percentage allocations.
Unfinished work may be requeued after a worker disconnect; execution is not
exactly-once. General task APIs support native workloads and local process,
command, and script execution subject to the task/runtime requirements.

Workers advertise CPU, RAM, operating system, architecture, and optional GPU
metadata. Jobs declare resource requirements; connected `READY` workers and
`BUSY` workers with remaining capacity are eligible, while `LOST` and
`OFFLINE` workers are not. Placement considers available CPU and RAM, and
worker state exposes allocated and remaining resources. `BUSY` means the
worker has no remaining schedulable capacity; it does not mean that only one
task is running. When no worker is registered yet or a suitable worker is
temporarily unavailable, work can remain queued. A job that cannot fit any
known worker's advertised capacity fails with a resource-requirement error.
GPU metadata can participate in requirement checks; it does not by itself
provide GPU workload execution or GPU-weighted automatic placement.

For partitionable native jobs, the Controller owns workload-specific
partitioning and reduction while the scheduler handles opaque payloads and
partial results. `sum` and `xor` split byte ranges; `dot_product` splits
corresponding vector ranges. Small inputs remain a single task; larger inputs
use bounded chunks that are assigned as worker capacity becomes available.
Automatic placement uses worker capacity, current allocations, telemetry,
and observed partition throughput. Manual distribution uses
`distribution_mode: "manual"` and `manual_allocations`; requested percentages
must total 100, and normal capacity/availability checks still apply.

Job and partition states are explicit, including queued, assigned, running,
completed, failed, cancelled, and requeued states where applicable. If a
worker disconnects, unfinished assignments can be requeued; completed
partitions are not resubmitted. Results arriving after cancellation or after
their assignment is no longer active are ignored. Since a disconnected worker
may have finished work before its result was received, retries do not provide
exactly-once execution.

The HTTP management API includes `/health`, `/v1/nodes`, `/v1/jobs`,
`/v1/jobs/{id}/partitions`, `/v1/tasks`, `/v1/artifacts`, and the event stream
at `/v1/events`. Resource, worker, partition, and job statistics are also
available through the node/job API endpoints. The Rust CLI uses that API.
Current command examples, as shown by `norden --help`, include:

```text
norden --help
norden status
norden workers list
norden jobs list
norden jobs partitions <job-id>
norden run sum 1 2 3
norden distribution show
```

Set `NODREN_CONTROLLER_URL` to the HTTP API URL; if unset, the CLI accepts
`NODREN_HTTP_ADDR` and otherwise defaults to `http://127.0.0.1:8080`.
`NODREN_JOB_TIMEOUT_SECS` sets the synchronous workload wait limit (default
30 seconds). `NODREN_HTTP_ADDR` also configures the Controller's HTTP bind
address; `NODREN_NODE_ADDR` configures its worker TCP bind address, and
`NODREN_STATE_FILE` selects its JSON state file.

`GET /v1/events` provides server-sent events for worker and job lifecycle,
progress, and partition changes. The Controller persists jobs, partitions,
payloads, results, and progress to its JSON state file when configured. On
restart, persisted workers become offline and unfinished work is eligible for
recovery after workers reconnect. Worker sessions use heartbeats and
reconnection; a task interrupted by a connection loss may run again, so the
system does not promise exactly-once execution.

General task requests can carry structured arguments, environment, timeout,
output limits, resource/target requirements, retry policy, and artifact
references. Artifacts are checksum-verified and transferred in bounded
protocol chunks for worker-side staging. Artifact distribution is not itself
computation distribution: an arbitrary process runs as a task on one worker
unless its workload has explicit partitioning semantics.
The Worker is not an OS sandbox: executable tasks run with the permissions of
the Worker process, so only use it in a trusted cluster and authorize the
workload sources accordingly.

Run Controller tests from `Backend/Controller-Go` with `go test ./...`.
Worker tests run from `Backend/Worker-Rust` with `cargo test` in an environment
that provides the target platform's native compiler toolchain.

For end-to-end cluster integration, the repository also has
`Tests/Integration/cluster_test.py`; it uses Python as test orchestration, not
as a Controller or Worker runtime component.
