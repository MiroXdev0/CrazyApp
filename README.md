# NODREN

### Distributed execution infrastructure for turning multiple machines into one compute platform.

[![Status](https://img.shields.io/badge/status-pre--release-00ff88?style=flat-square)](https://github.com/MiroXdev0/Nodren)
[![Go](https://img.shields.io/badge/Go-control--plane-00ADD8?style=flat-square\&logo=go\&logoColor=white)](https://go.dev/)
[![Rust](https://img.shields.io/badge/Rust-worker--runtime-000000?style=flat-square\&logo=rust\&logoColor=white)](https://www.rust-lang.org/)
[![C++](https://img.shields.io/badge/C%2B%2B-native--core-00599C?style=flat-square\&logo=c%2B%2B\&logoColor=white)](https://isocpp.org/)
[![C](https://img.shields.io/badge/C-low--level--runtime-A8B9CC?style=flat-square\&logo=c\&logoColor=black)](https://en.wikipedia.org/wiki/C_%28programming_language%29)
[![C%23](https://img.shields.io/badge/C%23-desktop--UI-512BD4?style=flat-square\&logo=csharp\&logoColor=white)](https://dotnet.microsoft.com/languages/csharp)

> **Nodren is a distributed execution platform designed to coordinate computation across multiple machines.**

Nodren provides a Controller, worker runtime, scheduler, artifact system, execution layer, native compute core, and CLI that work together to submit and execute workloads across a cluster.

**⚠️ Beta Release**
>
> Nodren is currently in **Beta**. The project is actively being developed, and some features may change, break, or be incomplete.
>
> **Linux support is still under development** and is not yet fully supported. Windows is currently the primary supported platform.
>
> If you encounter bugs or unexpected behavior, please report them through the project's issue tracker.

> Status: pre-release / active development**
> Windows x64 is currently the primary release platform. Linux x64 support exists but should be 
>  considered **beta / unstable**.

---

# `01` — What is Nodren?

Nodren is designed to make a collection of machines behave like a coordinated compute platform.

Instead of manually deciding which computer should run which workload:

```text
Machine A
Machine B
Machine C
Machine D
```

Nodren provides a central Controller that manages workers and assigns workloads:

```text
                         ┌───────────────────┐
                         │      NODREN       │
                         │    CONTROLLER     │
                         ├───────────────────┤
                         │ Scheduler         │
                         │ Job Management    │
                         │ Worker Registry   │
                         │ Artifact System   │
                         │ Resource Tracking │
                         └─────────┬─────────┘
                                   │
                         TCP / Binary Protocol
                                   │
                  ┌────────────────┼────────────────┐
                  ▼                ▼                ▼
             ┌─────────┐     ┌─────────┐     ┌─────────┐
             │ Worker  │     │ Worker  │     │ Worker  │
             │    A    │     │    B    │     │    C    │
             └────┬────┘     └────┬────┘     └────┬────┘
                  │               │               │
                  ▼               ▼               ▼
             Local CPU/GPU   Local CPU/GPU   Local CPU/GPU
```

The Controller manages **what should run and where**.

Workers manage **how workloads execute locally**.

The native core provides **low-level optimized computation** where a workload requires it.

---

# `02` — What Nodren Can Execute

Nodren is moving toward a generalized workload model rather than being limited to a few predefined mathematical operations.

The current execution foundation includes:

### Processes

Existing executables can be staged onto a worker and executed with controlled arguments and environment settings.

### Commands

Commands can be submitted to workers as executable tasks.

### Scripts

Script-based workloads can be executed through the worker runtime when the required runtime is available.

### Native workloads

Nodren has a native workload interface for workloads that explicitly provide partitioning and execution logic.

Current native workloads include:

```text
sum
xor
dot_product
```

### Files and artifacts

Nodren can transfer workload artifacts to workers using streamed transfers with checksum verification.

### Folders and projects

Project/folder workloads can be packaged and transferred as artifacts, including manifest information and executable entry points.

### AI / ML workloads

Nodren contains the foundation for generalized AI workload execution and resource-aware accelerator scheduling.

More advanced distributed AI strategies are still under development and should not be considered production-ready.

---

# `03` — Important: Artifact Distribution vs Compute Distribution

A fundamental design rule in Nodren is that **moving data is not the same as distributing computation**.

For example:

```text
program.exe
```

can be transferred to a worker and executed there.

But simply splitting:

```text
program.exe
```

into:

```text
chunk 1
chunk 2
chunk 3
```

does **not** make the program distributed.

Nodren therefore distinguishes between:

```text
Artifact distribution
        │
        └── Move files, programs, models,
            datasets, and packages

Computation distribution
        │
        └── Split actual work into independently
            executable partitions
```

A workload must provide an appropriate execution contract before Nodren can safely distribute its computation.

This distinction is especially important for arbitrary executables and AI workloads.

---

# `04` — Architecture

Nodren is divided into several major components.

```text
                         CLIENTS
                            │
             ┌──────────────┴──────────────┐
             │                             │
            CLI                         HTTP API
             │                             │
             └──────────────┬──────────────┘
                            ▼
                    ┌───────────────┐
                    │ GO CONTROLLER │
                    ├───────────────┤
                    │ Scheduler     │
                    │ Job Manager   │
                    │ Node Registry │
                    │ Resources     │
                    │ Artifacts     │
                    │ Recovery      │
                    │ HTTP API      │
                    └───────┬───────┘
                            │
                    Binary TCP Protocol
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
         ┌─────────┐   ┌─────────┐   ┌─────────┐
         │  Rust   │   │  Rust   │   │  Rust   │
         │ Worker  │   │ Worker  │   │ Worker  │
         └────┬────┘   └────┬────┘   └────┬────┘
              │             │             │
              ▼             ▼             ▼
         Local Runtime  Local Runtime  Local Runtime
              │             │             │
              ▼             ▼             ▼
          CPU / GPU      CPU / GPU      CPU / GPU
```

---

# `05` — Controller

The Controller is the central control plane.

It is implemented in **Go**.

Responsibilities include:

* worker registration
* worker health tracking
* scheduling
* resource accounting
* job management
* task assignment
* partition management
* artifact management
* result handling
* retry and recovery logic
* HTTP API
* persistent state
* cluster events
* worker capability tracking

The Controller does not perform the actual workload computation on behalf of workers.

Its job is to coordinate the cluster.

---

# `06` — Worker Runtime

The worker is implemented in **Rust**.

A worker connects to the Controller and waits for work.

The worker is responsible for:

* controller connection
* registration
* heartbeats
* hardware discovery
* resource reporting
* workload reception
* artifact staging
* process execution
* native workload execution
* result transmission
* cleanup
* reconnect handling
* execution limits
* local resource validation

A worker is intended to be independently deployable on machines contributing compute resources.

---

# `07` — Native Core

Nodren includes a native execution layer written across:

```text
C
C++
x86-64 Assembly
```

The native core provides a stable native ABI for workloads that require low-level execution.

It is intended for:

* optimized kernels
* low-level memory operations
* CPU-specific computation
* native workloads
* performance-sensitive execution paths

The native core is **not** responsible for every workload executed by Nodren.

General process, command, and script workloads can execute through the worker runtime without requiring the native core.

---

# `08` — Adaptive Scheduling

Nodren uses resource-aware scheduling rather than blindly distributing work equally.

Workers can report information such as:

```text
CPU
RAM
GPU
GPU VRAM
Architecture
Operating System
Current allocations
Capabilities
Availability
```

The scheduler can use these properties when determining where work can run.

For workloads that support partitioning, Nodren can use different partition sizes based on worker capacity.

For example:

```text
                 JOB
                  │
        ┌─────────┼─────────┐
        ▼         ▼         ▼
     Worker A  Worker B  Worker C
       60%        25%        15%
```

The exact distribution depends on workload requirements and available resources.

Nodren does **not** assume that every machine in a cluster has identical hardware.

---

# `09` — Reliability

Distributed systems fail.

Nodren is designed around that assumption.

Workers can disconnect, reconnect, become unavailable, or fail while work is being executed.

The Controller tracks execution state and can recover affected work when possible.

The reliability layer includes:

* persistent worker connections
* heartbeat monitoring
* reconnect handling
* worker state transitions
* stale result rejection
* duplicate result handling
* task requeueing
* resource accounting recovery
* persistent Controller state
* retry handling
* execution state tracking

Conceptually:

```text
             Worker A
                │
                │ executing
                ▼
              Task
                │
             disconnect
                X
                │
                ▼
          ┌─────────────┐
          │ Controller  │
          └──────┬──────┘
                 │
              requeue
                 │
          ┌──────┴──────┐
          ▼             ▼
       Worker B      Worker C
```

Recovery depends on the workload and its execution semantics.

---

# `10` — Artifact System

Nodren includes an artifact system for moving workload data between the Controller and workers.

Artifacts can represent:

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

The artifact layer supports:

* streamed transfer
* bounded chunk sizes
* checksum verification
* SHA-256 metadata
* content-addressed lookup
* deduplication
* worker staging
* cleanup
* package validation

The artifact system is separate from computation partitioning.

A large model, for example, can be transferred efficiently without implying that the model's computation is automatically distributed.

---

# `11` — AI / ML Direction

AI is an important long-term use case for Nodren.

The architecture is being developed toward workloads such as:

```text
Large-model inference
Batch inference
Embeddings
Model execution
Dataset processing
Distributed training
Distributed inference
GPU workloads
```

Nodren already has the beginnings of accelerator-aware scheduling and workload modeling.

Future distributed AI execution may involve strategies such as:

```text
Data Parallelism
Tensor Parallelism
Pipeline Parallelism
Distributed Inference
Distributed Training
```

However, these strategies require actual runtime and communication support.

Nodren will **not claim that an AI workload is distributed simply because multiple workers exist**.

The goal is to provide proper execution adapters, worker groups, resource discovery, artifact distribution, communication, checkpointing, and recovery before calling a distributed AI strategy complete.

---

# `12` — Networking

Nodren uses standard TCP/IP networking.

Workers can communicate with the Controller over:

```text
Ethernet
Wi-Fi
LAN
Direct Ethernet
```

No custom network hardware is required.

The worker maintains a persistent TCP connection to the Controller.

The control protocol follows the general pattern:

```text
REGISTER
   ↓
READY
   ↓
TASK
   ↓
EXECUTE
   ↓
RESULT
   ↓
HEARTBEAT
   ↕
HEARTBEAT_ACK
```

The Controller provides the HTTP API separately from the worker TCP protocol.

---

# `13` — Current Windows Runtime

The intended four-file Windows x64 release is:

```text
release/

├── nodren.exe
├── nodren.exe-CLI
├── nodren-worker.exe
└── nodren-core.dll
```

### `nodren.exe`

The main Nodren Controller/server executable.

It provides the backend services required to operate the cluster.

```text
Controller
Scheduler
Worker management
Job management
HTTP API
Worker TCP server
Artifact management
```

Default endpoints:

```text
Worker TCP    :9000
HTTP API      :8080
```

Run:

```powershell
.\nodren.exe
```

### `nodren.exe-CLI`

The command-line interface for controlling and interacting with Nodren.

Example:

```powershell
.\nodren.exe-CLI --help
```

The CLI communicates with the Controller rather than directly implementing cluster scheduling.

### `nodren-worker.exe`

The standalone worker runtime.

Example:

```powershell
.\nodren-worker.exe --controller 192.168.10.1:9000
```

### `nodren-core.dll`

The native core used by workloads that require the Nodren native execution layer.

It must be available to the worker in the expected runtime location.

---

# `14` — Desktop UI

Nodren has an **Avalonia desktop application as a development component**.

The UI is intentionally **not part of the four-file Windows release**.

The final runtime release does not contain:

```text
nodren-ui.exe
nodrenStart.exe
```

The UI communicates with the Controller through its HTTP API.

It is intended for development and cluster visualization, including areas such as:

```text
Dashboard
Workers
Jobs
Workloads
Distribution
Cluster status
Configuration
```

The UI is not required for the core Controller → Worker execution path.

---

# `15` — Language Stack

Nodren uses different languages at different system boundaries.

| Layer                | Technology           |
| -------------------- | -------------------- |
| Controller           | **Go**               |
| Worker runtime       | **Rust**             |
| Native engine        | **C / C++**          |
| Low-level hot paths  | **x86-64 Assembly**  |
| CLI                  | **Rust**             |
| Desktop UI           | **C# / Avalonia**    |
| Testing / automation | **Python**           |
| Web components       | **TypeScript / Bun** |

The goal is not to use as many languages as possible.

Each language exists because it fits a particular part of the system.

---

# `16` — Repository

```text
Nodren/

├── Backend/
│   ├── Controller-Go/
│   ├── Worker-Rust/
│   └── Web-Server/
│
├── CLI/
│   └── Rust/
│
├── Core/
│   ├── C/
│   ├── Cpp/
│   └── Assembly/
│
├── Frontend/
│   ├── Desktop/
│   └── Web/
│
├── Database/
├── Tests/
├── docs/
├── scripts/
└── tools/
```

The exact repository structure may evolve during development.

---

# `17` — Development

## Controller

```bash
cd Backend/Controller-Go

go test ./...
go vet ./...
go run .
```

## Worker

```bash
cd Backend/Worker-Rust

cargo test
cargo build
```

## Native Core

Windows:

```powershell
cd Core
.\build_core.ps1
```

Linux:

```bash
cd Core
./build_core.sh
```

## Release Build

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

## Integration Tests

```bash
python Tests/Integration/cluster_test.py
```

Some native Windows builds require the appropriate Microsoft C++ toolchain and Windows SDK.

---

# `18` — Verification

Nodren has automated testing across several layers.

Current verification includes areas such as:

```text
Go controller tests
Go vet
Controller build
Rust formatting
Rust metadata/type validation
Python syntax validation
Avalonia build
Artifact validation
Scheduler tests
Protocol validation
GPU/resource validation
Integration coverage
Release packaging validation
Clean-release validation
```

The project is still a **pre-release**.

A passing unit or packaging test does not automatically mean that every multi-machine workload scenario has been validated.

In particular, generalized execution and advanced distributed AI execution require additional end-to-end validation.

---

# `19` — Current Status

> **Nodren is a pre-release distributed execution platform under active development.**

The current system provides a functional foundation for coordinating workloads between a Controller and workers.

Implemented areas include:

* Controller / worker architecture
* persistent worker connections
* worker registration
* heartbeat monitoring
* resource-aware scheduling
* CPU/RAM capability tracking
* GPU capability modeling
* process execution
* command execution
* script execution
* native workloads
* artifact transfer
* folder/project packaging
* checksum verification
* workload requirements
* retries and recovery mechanisms
* partition management
* adaptive distribution infrastructure
* CLI
* HTTP API
* persistent Controller state
* failure detection and requeueing
* native execution core
* Windows x64 release packaging

The current generalized execution layer supports workload types including:

```text
PROCESS
COMMAND
SCRIPT
NATIVE_WORKLOAD
```

with artifact and package handling for their required inputs.

The native partitionable execution path currently includes:

```text
sum
xor
dot_product
```

Arbitrary executables are **not automatically converted into distributed workloads**.

The worker execution environment is also **not a security sandbox**. Running untrusted workloads should therefore be treated as unsafe.

---

# `20` — Roadmap

```text
[✓] Controller / Worker architecture
[✓] Persistent worker connections
[✓] Worker registration and heartbeats
[✓] Resource-aware scheduling
[✓] Concurrent execution infrastructure
[✓] Failure detection and recovery
[✓] Native execution core
[✓] Rust worker runtime
[✓] CLI
[✓] HTTP API
[✓] Artifact system foundation
[✓] Process / Command / Script execution
[✓] Adaptive distribution infrastructure
[✓] Manual distribution controls
[✓] GPU capability modeling
[✓] Windows four-file release packaging

[ ] Full physical multi-machine validation
[ ] Resumable artifact transfers
[ ] Output artifact collection
[ ] Expanded workload adapters
[ ] Production GPU execution
[ ] Distributed AI execution
[ ] Distributed training
[ ] Distributed inference
[ ] Checkpointing
[ ] Distributed storage
[ ] Sandboxing / isolation
[ ] SDK
[ ] Plugin system
[ ] Production deployment tooling
```

Roadmap items marked as incomplete are planned or under development and should not be interpreted as currently supported production features.

---

# `21` — Design Philosophy

### Compute should stay close to the hardware.

### Control should stay separate from execution.

### Distributed systems should expect failure.

### Work should move toward available capacity.

### Artifact distribution should not be confused with computation distribution.

### Performance should be measured, not assumed.

### Unsupported execution strategies should fail explicitly instead of pretending to work.

### Every language should have a reason to exist.

---

# Built by MiroXdev

Nodren is an independent systems project exploring how distributed execution can be built across multiple machines while keeping control, execution, networking, and low-level computation as separate architectural layers.

**Go · Rust · C · C++ · C# · TypeScript · Python · x86-64**

---

<p align="center">

<strong>NODREN</strong>

<br>

<sub>Distributed execution. One platform.</sub>

</p>
