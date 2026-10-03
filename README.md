# Nodren

> Distributed compute infrastructure for turning multiple machines into one execution platform.

Nodren is an active pre-release project for coordinating computation across heterogeneous machines. A central Controller manages workers, jobs, resources, partitioning, artifacts, and execution state while standalone Workers perform the actual work on their machines.

The project is built around a simple idea:

```text
                 NODREN CLUSTER

          ┌─────────────────────────┐
          │      Controller         │
          │ Go · scheduler · API    │
          └────────────┬────────────┘
                       │
                 TCP / binary
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
       Worker A     Worker B     Worker C
       Rust         Rust         Rust
          │            │            │
       CPU/GPU      CPU/GPU      CPU/GPU
```

Nodren is intended for real compute workloads rather than games or a single specialized application.

> **Status: Pre-release / active development**
>
> Windows x64 is the primary development and release target. Linux support is still beta/unstable. Advanced distributed AI execution is experimental.

---

## What Nodren is

Nodren separates **control** from **execution**.

The Controller decides:

* which workers are available
* what resources they provide
* where jobs should run
* how partitionable work is distributed
* how job state is tracked
* how failed/lost work is recovered
* how clients interact with the cluster

Workers handle:

* hardware/resource reporting
* persistent Controller connections
* workload reception
* artifact staging
* local process/script execution
* native workload execution
* result transmission
* cleanup and reconnect behavior

This lets a cluster contain machines with different CPU, RAM, GPU, and capability profiles.

---

## Architecture

```text
       Rust CLI                         Avalonia desktop application
      norden.exe                              Norden.exe
          │ HTTP                                  │ manages
          └────────────────┬──────────────────────┘
                           ▼
                  Go Controller / control plane
                   HTTP API + scheduler
                           │
                persistent TCP binary protocol
                           │
                      Rust Worker
                   norden-worker.exe
                           │ C ABI
                           ▼
          C / C++ / x86-64 Assembly native Core
                    norden-core.dll
```

### Main components

| Component    | Technology                | Responsibility                                 |
| ------------ | ------------------------- | ---------------------------------------------- |
| Controller   | Go                        | Cluster control plane, scheduler, API, jobs    |
| Worker       | Rust                      | Persistent TCP client, local workload execution |
| Native Core  | C / C++ / x86-64 Assembly | Native workloads behind the Worker C ABI      |
| CLI          | Rust                      | HTTP client for cluster control (`norden.exe`) |
| Desktop app  | C# / Avalonia             | UI plus managed Go Controller (`Norden.exe`)   |
| Web frontend | TypeScript / Bun          | Separate web client/frontend                   |
| Database     | SQL files                 | Schemas and initialization assets; not the Controller's state store |
| Tests/tools  | Python + Go + scripts     | Testing, diagnostics, benchmarking, automation |

The languages are intentionally split by responsibility rather than forcing the entire project into one language.

---

# Core concepts

## Controller

The Controller is the central coordination process.

Default endpoints:

```text
Worker TCP : 9000
HTTP API   : 8080
```

For a Controller started directly during development, the bind addresses and
state file can be changed through:

```text
NODREN_NODE_ADDR
NODREN_HTTP_ADDR
NODREN_STATE_FILE
```

`Norden.exe` manages its local Controller on `127.0.0.1:8080` and `:9000`.
Users launch only `Norden.exe`; they do not start a separate Controller
executable. The Rust CLI uses `NODREN_CONTROLLER_URL` for the HTTP API address,
falling back to `NODREN_HTTP_ADDR` and then `http://127.0.0.1:8080`.
`NODREN_JOB_TIMEOUT_SECS` controls synchronous CLI workload waits (default
30 seconds). These settings also apply to CLI requests made against a
development or remote Controller.

The Controller currently handles areas including:

* worker registration
* worker health/state
* resource tracking
* job creation
* scheduling
* partition assignment
* job control
* result collection
* event streaming
* artifact/workload handling
* persistent state
* retry/requeue behavior
* HTTP API access

The Controller is the control plane. It should not become the machine that performs all of the actual computation.

---

## Worker

The Worker is a standalone Rust runtime.

A worker connects to the Controller and reports information about the machine it is running on.

Worker responsibilities include:

* registration
* READY/BUSY/LOST/OFFLINE state handling
* heartbeats
* reconnect behavior
* CPU/RAM/GPU capability reporting
* resource validation
* workload execution
* native Core integration
* artifact staging
* result transmission
* cleanup

The Worker is designed to be deployable independently on machines contributing compute capacity.

---

## Native Core

Nodren Core is the low-level execution layer.

It provides a C-compatible boundary around native C/C++ implementations, with x86-64 assembly available for selected hot paths where profiling justifies it.

Current native workload examples include:

```text
sum
xor
dot_product
```

The Core is deliberately **not** responsible for:

* cluster scheduling
* worker selection
* network communication
* HTTP APIs
* artifact transfer
* desktop UI
* distributing arbitrary executables

The intended boundary is:

```text
Controller
    ↓
Rust Worker
    ↓
C ABI / FFI
    ↓
Nodren Core
    ↓
Native computation
```

This keeps the native layer focused on low-level execution.

---

# Workloads

Nodren supports a generalized workload model.

The runtime can represent workloads such as:

* native workloads
* existing executables/processes
* commands
* scripts
* files and folders
* project packages
* datasets
* model artifacts
* AI/ML workloads

However, **artifact distribution and computation distribution are different things**.

For example, copying a program to three workers does not automatically make that program distributed.

```text
Artifact distribution
    ↓
Move the required data/program/model

Computation distribution
    ↓
Split actual work into executable partitions
```

A workload must expose suitable execution/partition semantics before Nodren can safely divide its computation.

---

# Adaptive distribution

Partitionable workloads can be distributed according to worker capacity instead of simply giving every worker the same amount of work.

For example:

```text
                 JOB
                  │
       ┌──────────┼──────────┐
       ▼          ▼          ▼
   Worker A   Worker B   Worker C
      60%        25%        15%
```

The actual allocation depends on the workload and available resources.

The intended model is:

* stronger workers receive larger/harder partitions
* weaker workers receive smaller partitions
* unavailable workers are not assigned new work
* allocations can change as worker capacity changes
* partition progress is tracked independently

Nodren supports two distribution modes:

### Automatic

The scheduler determines distribution from worker capacity and workload information.

### Manual

The user can explicitly provide worker percentages.

The CLI exposes distribution controls:

```powershell
norden distribution show <job-id>
norden distribution auto <job-id>
norden distribution set <job-id> worker-a=60 worker-b=25 worker-c=15
```

The desktop UI also exposes automatic/manual distribution controls.

Manual allocations are intended for cases where the user needs direct control over how a partitionable workload is divided.

---

# Scheduling

Nodren uses resource-aware placement instead of treating every machine as identical.

Worker information can include:

```text
CPU cores
RAM
GPU capability
GPU memory/capabilities
Operating system
Architecture
Current allocations
Availability
Worker capabilities
Telemetry
```

The scheduler considers available capacity and workload requirements when assigning work.

A worker can be:

```text
READY
BUSY
LOST
OFFLINE
PAUSED
```

The exact scheduling decision is workload-dependent.

Nodren's goal is not to claim that a heterogeneous cluster is magically equivalent to identical hardware. The scheduler exists specifically because the machines are different.

---

# Reliability

Distributed execution assumes that machines can disappear.

Nodren therefore tracks worker and partition state rather than assuming every task completes successfully.

The system includes mechanisms for:

* heartbeats
* persistent worker sessions
* reconnect handling
* worker state transitions
* partition requeueing
* retry operations
* resource accounting recovery
* stale/lost worker detection
* result tracking
* persistent Controller state

A simplified failure path is:

```text
Worker A
   │
   │ executing partition
   ▼
 disconnect
   X
   │
   ▼
Controller detects loss
   │
   ▼
partition becomes requeued
   │
   ├───────────────┐
   ▼               ▼
Worker B        Worker C
```

Recovery depends on the workload's execution semantics. Not every arbitrary process can be resumed from an arbitrary point.

---

# Artifact distribution

Nodren can work with workload inputs that must be staged onto workers.

Artifacts may include:

```text
Executables
Scripts
Files
Folders
Projects
Models
Datasets
Packages
Other workload inputs
```

The artifact layer is intended to support:

* streamed transfer
* bounded transfer chunks
* checksum verification
* SHA-256 metadata
* content-addressed lookup
* deduplication
* worker staging
* cleanup

Again, transferring a model or dataset is not the same as distributing the computation performed on it.

---

# AI / ML

AI is one of the major long-term directions for Nodren.

The project is being developed toward workloads such as:

* large-model inference
* batch inference
* embeddings
* model execution
* dataset processing
* distributed inference
* distributed training
* GPU-heavy workloads

There is already experimental AI/model support in the project, including the ability to provide model/workload data for execution attempts.

However, this is **not yet a general-purpose distributed AI runtime**.

In particular, simply giving Nodren a model folder and several workers does not guarantee that the model will automatically split across those machines.

Proper distributed AI execution requires runtime-specific support for things such as:

```text
Data parallelism
Tensor parallelism
Pipeline parallelism
Worker groups
GPU placement
Inter-worker communication
Model sharding
Checkpointing
Recovery
Memory management
```

These are active development areas.

The goal is to make Nodren useful for large AI workloads without pretending that unsupported models are already distributed.

---

# Networking

The Controller and Workers communicate through a persistent TCP connection using Nodren's binary protocol.

The general lifecycle is:

```text
Worker starts
    ↓
REGISTER
    ↓
READY
    ↓
TASK / PARTITION
    ↓
EXECUTE
    ↓
RESULT
    ↓
HEARTBEAT
    ↕
HEARTBEAT_ACK
```

Nodren is intended to work across normal IP networking, including:

* Ethernet
* Wi-Fi
* LANs
* direct Ethernet connections

Local-first operation is also important: a Nodren cluster does not need an internet connection just to operate locally.

Longer-term networking work includes simpler direct/cable-based setups and optional online connectivity through user-controlled infrastructure.

---

# CLI

The Rust CLI is the primary command-line interface for interacting with a running Controller.

The Windows release CLI executable is `norden.exe`. Add the release directory
to `PATH` to run it as `norden`:

```text
norden.exe
```

Examples:

```text
norden --help
norden status
norden devices
norden workers list
norden workers stats
norden workers info <worker-id>
norden workers ping <worker-id>
norden jobs list
norden jobs info <job-id>
norden jobs stats <job-id>
norden jobs partitions <job-id>
norden tasks list
norden run sum 1 2 3
norden run xor 1 2 3
norden run dot_product 1,2 3,4
norden run process <executable> -- <arguments...>
norden run command <executable> -- <arguments...>
norden run script <runtime> <script> -- <arguments...>
norden distribution show
norden distribution auto <job-id>
norden distribution set <job-id> worker-a=60 worker-b=40
norden monitor
norden doctor
norden config
norden version
```

`norden --help` prints the supported commands. Job/task controls also include
the forms listed there, such as `jobs cancel`, `tasks result`, and
`tasks retry`.

---

# Windows release

The Windows x64 release contains exactly these four user-facing files:

```text
release/
├── Norden.exe
├── norden.exe
├── norden-worker.exe
└── norden-core.dll
```

Windows treats filenames case-insensitively by default, so the release directory must have NTFS per-directory case sensitivity enabled to keep `Norden.exe` (the desktop app) distinct from `norden.exe` (the CLI). The build script configures this on the release folder. If the workspace location does not permit it (for example, a protected or synced folder), it writes the release to `%LOCALAPPDATA%\Nodren\release` and prints the actual path.

### `Norden.exe`

The main Nodren desktop application. It starts the Avalonia UI and manages
the Go Controller lifecycle internally, including clean Controller shutdown
when the application exits. The embedded Controller is extracted to the
user's local Nodren data directory. Users launch only `Norden.exe`; there is
no separate Controller executable in the release folder.

The Controller provides:

* Controller
* scheduler
* worker management
* job management
* HTTP API
* worker TCP server
* state persistence

Launch the complete application:

```powershell
.\Norden.exe
```

Default addresses:

```text
TCP :9000
HTTP :8080
```

### `norden.exe`

The standalone Rust CLI used to control and inspect Nodren. Add the release
directory to `PATH` to invoke it as `norden`.

It communicates with the Controller.

### `norden-worker.exe`

The standalone Rust worker runtime. It connects to the Controller's TCP
listener, receives assigned jobs/tasks, reports heartbeats and results, and
reconnects according to the worker runtime's existing retry behavior. It
loads `norden-core.dll` from beside its executable for native workloads.

Example:

```powershell
norden-worker.exe --controller 192.168.1.10:9000
```

Use the current worker help output for the exact supported arguments.

### `norden-core.dll`

The native Core library consumed by the Worker through the existing C ABI.
The Worker resolves this DLL beside its own executable.

---

# Desktop UI

`Norden.exe` is the packaged Avalonia desktop application. It manages a local
Go Controller automatically. The UI provides:

* cluster dashboard
* worker monitoring
* job monitoring
* workload submission
* distribution controls
* worker actions
* job actions
* Controller settings
* live state/event updates

The current UI includes automatic/manual distribution controls and displays partition progress and scheduler information.

The desktop app is not required for the Controller → Worker execution path;
the CLI and remote Workers can use the Controller API independently.

---

# Repository layout

The repository is organized around the major runtime layers:

```text
Nodren/
│
├── Backend/
│   ├── Controller-Go/       # Go Controller
│   ├── Worker-Rust/         # Rust Worker
│   └── README.md            # Backend documentation
│
├── CLI/
│   └── Rust/                # Rust CLI
│
├── Core/                    # Native execution layer
│
├── Native/                  # Native runtime support
│
├── Frontend/
│   ├── Desktop/             # Avalonia desktop UI
│   └── Web/                 # TypeScript/Bun web client
│
├── Database/                # SQL initialization, schemas and migrations
├── Python/                  # AI, analytics, scripts and tooling
├── Tests/                   # Integration and system tests
├── docs/                    # Architecture and roadmap
├── scripts/                 # Build and release scripts
├── tools/                   # Developer/benchmark utilities
└── LICENSE
```

Some directories contain work-in-progress or compatibility code. The repository is actively changing, so the layout should not be treated as a frozen API.

The Go Controller currently persists its runtime state to a JSON state file
(`NODREN_STATE_FILE` when configured). SQL files under `Database/` are
database assets and are not a configured SQL backend for the Controller.

---

# Building

## Controller

From the repository root:

```powershell
cd Backend/Controller-Go
go test ./...
go vet ./...
go build .
```

The Controller module declares Go 1.23. Building/running this component
directly is for development; the Windows end-user app embeds and manages it.

---

## Worker

```powershell
cd Backend/Worker-Rust
cargo test
cargo build
```

On Windows, run these commands in a Visual Studio Developer PowerShell session
with LLVM `clang` available on `PATH`; the Worker build script compiles and
links the native Core.

For the Windows release, use the root release script so the native Core DLL
is built, exported, and packaged beside the Worker:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

---

## CLI

```powershell
cd CLI/Rust
cargo test
cargo build --release
```

The current CLI package version is `0.2.0`.

---

## Native Core

Windows:

```powershell
cd Core
.\build_core.ps1
```

The Windows script builds the release DLL as part of the Worker build. The
standalone Core script builds/runs native self-tests; it does not produce the
user-facing release on its own and requires its GCC/G++ toolchain on `PATH`.
Linux native development build:

```bash
cd Core
bash ./build_core.sh
```

The Windows release build requires Go, Rust, .NET 10, Visual Studio C++ Build
Tools, and LLVM `clang`. The Linux scripts currently describe a separate
beta/unstable packaging path; the four-file release documented above is the
Windows release.

---

## Complete Windows release

From the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

The Windows build requires Go, Rust, .NET 10, Visual Studio C++ Build Tools, and LLVM `clang` (for the native assembly kernel). Pass `-ReleaseDirectory <path>` to select an output directory; it must support NTFS per-directory case sensitivity.

The script builds and validates exactly these Windows artifacts:

```text
Norden.exe
norden.exe
norden-worker.exe
norden-core.dll
```

Add the release directory to `PATH` to invoke the CLI as `norden`. Windows is
case-insensitive by default, so the output directory needs NTFS
per-directory case sensitivity to retain the distinct `Norden.exe` and
`norden.exe` names. The build script reports its output path and uses
`%LOCALAPPDATA%\Nodren\release` if the workspace does not permit that setting.

Validate a copied, source-independent release directory with:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\test-clean-release.ps1
```

---

# Testing

Nodren contains tests across multiple layers.

Examples include:

```text
Controller unit/API tests
Scheduler tests
Distributed execution tests
Worker reconnect/recovery tests
Protocol validation
Native workload tests
Artifact tests
Release packaging tests
Linux release checks
Python integration/orchestration tests
```

Integration tests are located under:

```text
Tests/
Tests/Integration/
```

A representative integration entry point is:

```powershell
python Tests/Integration/cluster_test.py
```

Python is used for testing/tooling in this project; it is not the core runtime of Nodren.

---

# Development philosophy

Nodren is intentionally a low-level, systems-oriented project.

The project prioritizes:

* actual distributed execution over superficial cluster UI
* resource-aware scheduling
* explicit runtime boundaries
* native performance where it matters
* heterogeneous hardware support
* fault handling
* measurable performance
* correctness before optimization
* honest capability reporting

The native Core follows the general rule:

```text
Implement
   ↓
Test correctness
   ↓
Benchmark
   ↓
Profile
   ↓
Optimize
   ↓
Test again
```

Assembly is useful when it solves a measured bottleneck, not simply because the project contains assembly.

---

# Project direction

Nodren is being developed toward a broader compute platform with several layers:

### 1. Distributed execution

Make multiple machines usable as one coordinated compute environment.

### 2. Better heterogeneous scheduling

Use real worker capabilities to decide where and how much work should run.

### 3. Adaptive partitioning

Give stronger machines larger partitions while allowing manual overrides when users need direct control.

### 4. Large workload handling

Improve artifact, model, dataset, and result movement without confusing data movement with computation distribution.

### 5. AI / ML execution

Build proper execution adapters and distributed runtime support for AI workloads rather than relying on unsupported automatic splitting.

### 6. Developer ecosystem

Longer-term work includes SDKs, plugins, deployment tooling, automation, and richer client interfaces.

---

# What Nodren is not

Nodren is not:

* a game engine
* a game networking framework
* simply a remote command launcher
* merely a file-transfer tool
* a website with a cluster dashboard attached
* a claim that arbitrary programs automatically become distributed
* a claim that any AI model automatically scales across every connected machine

The hard part of Nodren is the execution fabric underneath the interface.

---

# Current status

Nodren is a **pre-release** system with a working foundation for distributed execution.

The repository currently contains:

* a Go Controller
* a Rust Worker
* a Rust CLI
* a native Core
* resource-aware scheduling
* worker health/reconnect behavior
* job and task control
* partition tracking
* automatic/manual distribution controls
* HTTP API
* event streaming
* an Avalonia desktop client
* integration and system tests
* experimental AI/model workload work

Advanced features, especially generalized distributed AI execution, remain under active development.

---

# License

Nodren is distributed under the license in [LICENSE](LICENSE).

---

## Project

**Nodren**

Distributed execution infrastructure for turning multiple machines into one execution platform.

Repository: https://github.com/MiroXdev0/Nodren
