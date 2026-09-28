# NODREN

### Distributed compute infrastructure for turning multiple machines into one execution platform.

[![Status](https://img.shields.io/badge/status-pre--release-00ff88?style=flat-square)](https://github.com/MiroXdev0/Nodren)
[![Go](https://img.shields.io/badge/Go-control--plane-00ADD8?style=flat-square\&logo=go\&logoColor=white)](https://go.dev/)
[![Rust](https://img.shields.io/badge/Rust-worker--runtime-000000?style=flat-square\&logo=rust\&logoColor=white)](https://www.rust-lang.org/)
[![C++](https://img.shields.io/badge/C%2B%2B-native--core-00599C?style=flat-square\&logo=c%2B%2B\&logoColor=white)](https://isocpp.org/)
[![C](https://img.shields.io/badge/C-low--level--runtime-A8B9CC?style=flat-square\&logo=c\&logoColor=black)](https://en.wikipedia.org/wiki/C_%28programming_language%29)
[![C%23](https://img.shields.io/badge/C%23-desktop--UI-512BD4?style=flat-square\&logo=csharp\&logoColor=white)](https://dotnet.microsoft.com/languages/csharp)

---

## `>_` What is Nodren?

Nodren is a **distributed computing platform** designed to combine multiple machines into a single execution system.

Instead of treating every computer as an isolated machine:

```text
Machine A
Machine B
Machine C
Machine D
```

Nodren turns them into:

```text
                 ┌─────────────────┐
                 │     NODREN      │
                 │   CONTROLLER    │
                 └────────┬────────┘
                          │
             ┌────────────┼────────────┐
             │            │            │
             ▼            ▼            ▼
         ┌───────┐    ┌───────┐    ┌───────┐
         │Worker │    │Worker │    │Worker │
         │   A   │    │   B   │    │   C   │
         └───────┘    └───────┘    └───────┘
             │            │            │
             ▼            ▼            ▼
           Native       Native       Native
            Core         Core         Core
```

The Controller decides **where work goes**.

Workers decide **how it runs**.

The native core handles **the expensive computation**.

---

# `01` — Core Architecture

Nodren is split into independent layers.

```text
                         CLIENTS
                            │
                 ┌──────────┴──────────┐
                 │                     │
              CLI / UI              HTTP API
                 │                     │
                 └──────────┬──────────┘
                            ▼
                    ┌───────────────┐
                    │   GO CONTROL  │
                    │     PLANE     │
                    ├───────────────┤
                    │ Scheduler     │
                    │ Job Queue     │
                    │ Node Registry │
                    │ Health        │
                    │ Aggregation   │
                    └───────┬───────┘
                            │
                     Binary Protocol
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
          ┌────────┐    ┌────────┐    ┌────────┐
          │ RUST   │    │ RUST   │    │ RUST   │
          │ WORKER │    │ WORKER │    │ WORKER │
          └───┬────┘    └───┬────┘    └───┬────┘
              │              │              │
              ▼              ▼              ▼
          ┌────────┐     ┌────────┐     ┌────────┐
          │ C/C++  │     │ C/C++  │     │ C/C++  │
          │  CORE  │     │  CORE  │     │  CORE  │
          └────────┘     └────────┘     └────────┘
```

### Control plane

**Go**

Handles:

* worker registration
* scheduling
* resource accounting
* job queues
* worker health
* task assignment
* result aggregation
* HTTP API
* failure recovery

### Worker runtime

**Rust**

Handles:

* hardware discovery
* controller connection
* heartbeats
* task reception
* execution management
* resource validation
* result transmission

### Native execution

**C / C++ / x86-64 Assembly**

Handles:

* low-level memory
* task execution
* worker pools
* CPU dispatch
* optimized kernels
* stable native ABI

### Desktop interface

**C# / Avalonia**

Provides:

* cluster dashboard
* worker monitoring
* job monitoring
* workload submission
* adaptive distribution controls
* connection/configuration management

---

# `02` — Adaptive Work Distribution

One of Nodren's core features is **adaptive workload distribution**.

A workload does not have to be divided equally.

If the cluster looks like:

```text
Worker A    ████████████████████  16 CPU cores
Worker B    ███████████           8 CPU cores
Worker C    ████                  4 CPU cores
```

Nodren can distribute work according to available capacity:

```text
                 JOB
                  │
          ┌───────┼────────┐
          ▼       ▼        ▼
       Worker A Worker B Worker C
         57%      29%      14%
```

As workers finish, remaining work can be redistributed dynamically.

The scheduler considers:

* CPU capacity
* RAM capacity
* current allocations
* worker availability
* workload requirements
* completed work
* remaining partitions

Small workloads can remain as a single task instead of being unnecessarily fragmented.

Manual distribution is also supported when deterministic partitioning is required.

---

# `03` — Fault Recovery

Nodren assumes that distributed systems **will fail**.

Workers can disconnect while jobs are running.

The Controller can detect the failure, release the worker's allocations, and requeue affected work.

```text
             WORKER A
                │
                │ running
                ▼
              TASK
                │
             X disconnect
                │
                ▼
          ┌─────────────┐
          │  Controller │
          └──────┬──────┘
                 │
              requeue
                 │
        ┌────────┴────────┐
        ▼                 ▼
     Worker B          Worker C
```

The system also handles:

* reconnecting workers
* stale results
* heartbeat failures
* resource accounting recovery
* task requeueing
* worker replacement by ID

---

# `04` — Current Workloads

Nodren currently supports partitionable workloads including:

```text
sum
xor
dot_product
```

Example:

```bash
nodren run sum 1 2 3 4 5
```

```text
1 + 2 + 3 + 4 + 5
          ↓
         15
```

Vector workloads:

```bash
nodren run dot_product 1,2,3 4,5,6
```

```text
(1×4) + (2×5) + (3×6)
          ↓
          32
```

Workloads have their own partitioning and reduction logic.

This is important because **arbitrary programs cannot automatically be split into distributed tasks**. Nodren's workload system provides the contract that defines how work can be partitioned and reconstructed.

---

# `05` — Runtime

A Nodren deployment currently consists of:

```text
release/
│
├── nodren.exe
├── nodren-worker.exe
├── nodren-ui.exe
└── nodren_core.dll
```

### Controller

```bash
nodren.exe
```

Default endpoints:

```text
Worker TCP     :9000
HTTP API       :8080
```

### Worker

```bash
nodren-worker.exe --controller 192.168.10.1:9000
```

### Desktop UI

```bash
nodren-ui.exe
```

Self-test:

```bash
nodren-ui.exe --self-test
```

---

# `06` — Networking

Nodren uses normal TCP/IP networking.

That means workers can communicate over:

```text
Ethernet
Wi-Fi
LAN
Direct Ethernet
```

No special networking hardware or protocol is required.

The worker maintains a persistent connection to the Controller.

```text
REGISTER
   ↓
READY
   ↓
TASK_BATCH
   ↓
EXECUTE
   ↓
TASK_RESULT_BATCH
   ↓
HEARTBEAT
   ↕
HEARTBEAT_ACK
```

---

# `07` — Resource-Aware Scheduling

Workers advertise their capabilities:

```text
CPU cores
RAM
GPU vendor
GPU model
GPU VRAM
Architecture
Operating system
Current allocations
```

The scheduler uses those values to determine whether a worker can accept additional work.

Example:

```text
Worker A
CPU: 16 / 16
RAM: 32 GB
Status: READY

Worker B
CPU: 8 / 8
RAM: 16 GB
Status: READY

Worker C
CPU: 4 / 4
RAM: 8 GB
Status: BUSY
```

Jobs are assigned only when their resource requirements can be satisfied.

---

# `08` — Language Stack

Nodren deliberately uses different languages for different system boundaries.

| Layer                | Technology          |
| -------------------- | ------------------- |
| Controller           | **Go**              |
| Worker               | **Rust**            |
| Native engine        | **C++**             |
| Memory / ABI         | **C**               |
| Hot paths            | **x86-64 Assembly** |
| Desktop UI           | **C# / Avalonia**   |
| Testing / automation | **Python**          |

The goal is not to use more languages.

The goal is to give each layer the language that fits its job.

---

# `09` — Repository

```text
Nodren/
│
├── Backend/
│   ├── Controller-Go/
│   ├── Worker-Rust/
│   └── Web-Server/
│
├── Core/
│   ├── C/
│   ├── Cpp/
│   └── Assembly/
│
├── CLI/
│   └── Rust/
│
├── Frontend/
│   └── Desktop/
│
├── Tests/
├── docs/
├── scripts/
└── tools/
```

---

# `10` — Development

### Controller

```bash
cd Backend/Controller-Go
go test ./...
go run .
```

### Worker

```bash
cd Backend/Worker-Rust
cargo test
cargo build
```

### Native Core

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

### Full release

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
```

### Integration test

```bash
python Tests/Integration/cluster_test.py
```

---

# `11` — Verification

The project currently has automated coverage across the major layers.

```text
┌──────────────────────────────┐
│          VERIFICATION        │
├──────────────────────────────┤
│ ✓ Go controller tests        │
│ ✓ Go race detector           │
│ ✓ Rust worker tests          │
│ ✓ Rust CLI tests             │
│ ✓ Native core self-test      │
│ ✓ Python integration tests   │
│ ✓ C# UI build                │
│ ✓ UI self-test               │
│ ✓ Release packaging          │
│ ✓ Clean release verification │
│ ✓ Adaptive distribution      │
│ ✓ Failure recovery           │
└──────────────────────────────┘
```

---

# `12` — Status

> **Nodren is currently a pre-release systems project.**

The core distributed execution path is implemented and tested.

Current focus areas include:

* physical multi-machine validation
* deeper workload APIs
* advanced observability
* security and isolation
* more workload types
* GPU execution
* deployment tooling
* distributed storage
* checkpointing

---

# `13` — Roadmap

```text
[✓] Controller / Worker architecture
[✓] Persistent worker connections
[✓] Resource-aware scheduling
[✓] Concurrent execution
[✓] Failure recovery
[✓] Native execution core
[✓] Rust worker runtime
[✓] CLI
[✓] Desktop dashboard
[✓] Adaptive workload distribution
[✓] Manual distribution control

[ ] GPU workload execution
[ ] Advanced observability
[ ] Sandboxing
[ ] Distributed storage
[ ] Checkpointing
[ ] SDK
[ ] Plugin system
[ ] Production deployment
```

---

# `14` — Design Philosophy

### Compute should stay close to the hardware.

### Control should stay separate from execution.

### Distributed systems should expect failure.

### Work should move toward available capacity.

### Performance should be measured, not assumed.

### Every language should have a reason to exist.

---

## Built by MiroXdev

Nodren is an independent systems project exploring what a modern distributed compute platform can look like when the entire stack is under one architecture.

**Go · Rust · C · C++ · C# · x86-64**

---

<p align="center">

**NODREN**

<sub>Distributed compute. One platform.</sub>

</p>
