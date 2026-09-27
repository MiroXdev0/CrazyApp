# Nodren

**Distributed compute infrastructure for turning multiple machines into one execution platform.**

Nodren is a systems project for coordinating heterogeneous computers as a shared computational system. It separates orchestration, transport, native execution, low-level memory management, and developer tooling so each layer can be optimized independently.

> **Status:** Active development — architecture and core runtime are still evolving.

---

## What is Nodren?

Nodren is built around one idea:

> **Take multiple ordinary machines and make them behave like one compute platform.**

A controller manages the cluster and decides where work should run. Workers execute that work locally. The native core handles performance-critical computation without pushing the hot path through a high-level control layer.

```text
Client
  │
  ▼
Controller
  │
  ├── Node Registry
  ├── Scheduler
  ├── Job Queue
  ├── Health Tracking
  └── Result Aggregation
          │
          ├──────────────┬──────────────┐
          ▼              ▼              ▼
       Worker 1       Worker 2       Worker N
          │              │              │
          ▼              ▼              ▼
       Native Core   Native Core   Native Core
          │              │              │
          └──────────────┼──────────────┘
                         ▼
                  Result Assembly
```

---

## Architecture

Nodren is divided into a **control plane** and an **execution plane**.

### Control Plane

The control plane decides **what should happen and where it should happen**.

The Go controller handles:

* Node registration
* Node health
* Job queues
* Resource-aware placement
* Persistent worker sessions
* Binary framing
* Task/result tracking
* Retry and requeue handling

### Execution Plane

Workers decide **how work is executed locally**.

The Rust worker handles:

* Machine/resource discovery
* Worker registration
* Resource validation
* Persistent controller connections
* Heartbeats
* Task-batch decoding
* Local execution
* Result batching

### Native Core

The native core is deliberately separated from the controller and worker.

```text
C
│
├── Raw virtual memory
├── Aligned allocation
├── Arenas
└── Low-level ABI
        │
        ▼
C++
│
├── Task queues
├── Worker threads
├── Execution engine
└── CPU dispatch
        │
        ▼
x86-64 Assembly
│
└── Measured CPU hot paths
```

---

## Repository Structure

```text
Nodren/
│
├── Backend/
│   ├── Controller-Go/       # Go control plane
│   ├── Protocol/            # Backend protocol definitions
│   ├── Web-Server/
│   │   ├── Gateway/         # Web/API gateway
│   │   └── Runtime/         # Rust web runtime
│   └── Worker-Rust/         # Rust execution worker
│
├── CLI/
│   └── Rust/                # Native command-line tooling
│
├── Core/
│   ├── C/                   # Low-level memory/runtime
│   ├── Cpp/                 # Native execution engine
│   ├── Assembly/
│   │   └── X64/             # x86-64 optimized kernels
│   └── build_core.*         # Core build scripts
│
├── Shared/
│   ├── C_API/               # C ABI
│   ├── IPC/                 # IPC definitions
│   └── Protocol/            # Shared protocol types
│
├── Tests/                   # Integration and system tests
├── docs/                    # Architecture and roadmap
├── tools/                   # Benchmarks and developer tools
├── scripts/                 # Automation
│
├── .gitattributes
├── .gitignore
└── README.md
```

---

## Communication

Nodren uses persistent connections between the controller and workers instead of creating a new connection or process for every task.

A simplified session looks like:

```text
Controller
    │
    │ REGISTER
    ▼
  Worker
    │
    │ READY
    ▼
Controller
    │
    │ TASK_BATCH
    ▼
  Worker
    │
    │ Native Execution
    ▼
  Core
    │
    │ TASK_RESULT_BATCH
    ▼
Controller
```

The protocol includes messages such as:

```text
HELLO
REGISTER
READY
TASK_BATCH
TASK_RESULT_BATCH
HEARTBEAT
HEARTBEAT_ACK
ERROR
GOODBYE
```

Tasks contain identifiers so results can be associated with their original jobs independently of execution order.

---

## Language Roles

Nodren intentionally uses different languages for different layers.

| Language            | Responsibility                                      |
| ------------------- | --------------------------------------------------- |
| **Go**              | Controller, orchestration, scheduling and transport |
| **Rust**            | Worker runtime and systems components               |
| **C++**             | Native execution, task queues and worker pools      |
| **C**               | Memory primitives and stable native ABI             |
| **x86-64 Assembly** | Measured CPU hot paths                              |
| **Python**          | Testing, analysis, automation and tooling           |
| **C#**              | Management/desktop tooling where applicable         |

The goal is not to use many languages simply for the sake of using them.

Each language has a defined boundary and responsibility.

---

# Native Core

The native core contains three major layers.

## C

The C layer provides low-level primitives:

* Virtual-memory-backed allocation
* Aligned allocation
* Bump arenas
* Atomic memory statistics
* Stable C ABI

The execution path is designed to avoid allocating memory for every task.

## C++

The C++ layer owns:

* Native task execution
* Bounded task transport
* Worker threads
* CPU feature dispatch
* C ABI integration

The hot path uses POD task descriptors and avoids unnecessary:

* `std::function`
* JSON
* Per-task heap allocation
* Process creation

## Assembly

Assembly is reserved for CPU hot paths where profiling shows that it is justified.

The current x86-64 implementation contains an AVX2 reduction kernel.

Runtime dispatch determines whether the CPU supports the optimized implementation.

---

# Backend

The backend architecture is:

```text
                 HTTP / JSON
Web / Desktop ────────────────> Go Controller
                                   │
                                   │ Persistent TCP
                                   │ Binary Protocol
                                   ▼
                               Rust Worker
                                   │
                                   ▼
                             Local Execution
```

## Go Controller

Location:

```text
Backend/Controller-Go
```

Run tests:

```bash
cd Backend/Controller-Go
go test ./...
```

Run the controller:

```bash
go run .
```

Default addresses:

```text
Node TCP:  :9000
HTTP API:  :8080
```

Environment variables:

```text
NODREN_NODE_ADDR
NODREN_HTTP_ADDR
```

---

## Rust Worker

Location:

```text
Backend/Worker-Rust
```

Run:

```bash
cd Backend/Worker-Rust
cargo run -- --controller=127.0.0.1:9000
```

Available options include:

```text
--id=<node-id>
--controller=<host:port>
--ram-gb=<value>
--gpu-vendor=<vendor>
--gpu-model=<model>
--gpu-vram-gb=<value>
```

The worker is responsible for validating resources, maintaining its controller connection, receiving work, executing tasks, and returning results.

---

# Example Job

A job can be submitted through the controller API.

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

The execution flow is:

```text
HTTP Request
     │
     ▼
Controller
     │
     ▼
Scheduler
     │
     ▼
Worker
     │
     ▼
Native Runtime
     │
     ▼
Result
     │
     ▼
Controller
```

The native C/C++ layer can be connected behind the worker through the C ABI.

---

# Scheduling

Workers report information about their available resources.

Examples include:

```text
CPU cores
RAM
GPU vendor
GPU model
GPU VRAM
Architecture
Operating system
Network address
Current availability
Current workload
```

The scheduler can use these values together with:

```text
Job priority
Resource requirements
Queue length
Current load
Architecture compatibility
Worker availability
```

Planned scheduling strategies include:

```text
Round Robin
Highest Capacity
Priority First
Affinity Based
Resource Aware
```

---

# Fault Tolerance

Distributed systems fail.

Nodren is designed around that assumption.

Potential failure conditions include:

```text
Worker crash
Network partition
Timeout
Resource exhaustion
Partial result loss
Corrupted output
Worker overload
```

Possible recovery mechanisms include:

```text
Retry
Requeue
Failover
Checkpoint restore
```

A failed worker should not automatically require the entire workload to be discarded.

---

# Performance

Nodren treats performance as something that must be **measured**.

The repository contains benchmarking and diagnostic tooling for measuring:

```text
Execution time
Tasks / second
Operations / second
Queue wait
Transport overhead
IPC overhead
Native execution time
Result collection time
Latency
CPU utilization
Memory usage
Scaling efficiency
```

Scaling experiments can compare configurations such as:

```text
1 node
2 nodes
4 nodes
8 nodes
```

while keeping the workload consistent.

The same principle applies to the native runtime.

Low-level optimizations should be supported by profiling or benchmarks rather than assumptions.

---

# Error Model

Errors are represented as structured system events.

An error can contain:

```text
Error code
Component
Severity
Message
Node ID
Job ID
Task ID
Timestamp
Recoverability
```

Potential sources include:

```text
Networking
Protocol
Nodes
Jobs
Tasks
Resources
Memory
Native execution
```

Recoverable failures can be retried or rescheduled.

Unrecoverable failures can terminate the affected task or job.

---

# Development Status

Nodren is currently under active systems development.

### Implemented / In Progress

* Controller/worker architecture
* Node registration
* Persistent communication
* Framed binary transport
* Task batching
* Result batching
* Native C/C++ execution components
* Low-level memory primitives
* x86-64 CPU kernels
* Structured error handling
* Benchmark tooling
* Integration tests

### Still Being Developed

* Complete distributed scheduling
* Production resource management
* Multi-machine execution
* Task isolation
* Sandboxing
* Distributed storage
* Checkpointing
* Distributed memory
* Advanced observability
* SDK
* Higher-level workload APIs
* Production deployment tooling

---

# Roadmap

## Phase 1 — Foundation

* Node registration
* Worker heartbeat
* Job submission
* Result collection
* Controller/worker API

## Phase 2 — Scheduling

* Resource-aware placement
* Priority scheduling
* Retry logic
* Worker failure handling

## Phase 3 — Storage & Memory

* Job staging
* Result storage
* Checkpointing
* Distributed memory regions

## Phase 4 — Observability

* Cluster metrics
* Node health
* Logs
* Dashboards

## Phase 5 — Platform

* Sandboxing
* Workload isolation
* Security boundaries
* Remote deployment

## Phase 6 — Developer Ecosystem

* CLI
* SDK
* Plugin interfaces
* Automation workflows

---

# Design Principles

## Separate orchestration from computation

The controller should coordinate work rather than perform expensive computation itself.

## Keep the hot path native

High-frequency computation should not depend on JSON, unnecessary process creation, or high-level allocation patterns.

## Persistent communication

Workers maintain long-lived sessions with the controller.

## Batch work

Batching reduces communication and scheduling overhead.

## Backpressure

Workers should be able to communicate capacity and saturation instead of being continuously overloaded.

## Measure before optimizing

Performance changes should be supported by benchmarks or profiling.

## Test failure

A distributed system should be tested under failures, not only when every machine is healthy.

## Explicit boundaries

Each language and subsystem should have a defined responsibility and communication boundary.

---

# Documentation

Additional documentation:

* [Architecture](docs/architecture.md)
* [Core Architecture](docs/core-architecture.md)
* [Roadmap](docs/roadmap.md)
* [Backend](Backend/README.md)
* [Native Core](Core/README.md)
* [C Layer](Core/C/README.md)
* [C++ Layer](Core/Cpp/README.md)
* [x86-64 Assembly](Core/Assembly/X64/README.md)

---

# Building

Nodren currently consists of several independently buildable subsystems.

## Go Controller

```bash
cd Backend/Controller-Go
go test ./...
go run .
```

## Rust Worker

```bash
cd Backend/Worker-Rust
cargo build
cargo test
```

## Native Core — Windows

```powershell
cd Core
./build_core.ps1
```

## Native Core — Linux

```bash
cd Core
./build_core.sh
```

Build requirements may change while Nodren is under active development.

Generated binaries and build directories should not be committed to the source repository.

---

# Long-Term Goal

The long-term goal is to turn Nodren into a general-purpose compute fabric.

The intended workload lifecycle is:

```text
Submit
  ↓
Queue
  ↓
Inspect Resources
  ↓
Partition
  ↓
Schedule
  ↓
Execute
  ↓
Collect
  ↓
Reconstruct
  ↓
Complete
```

The system should eventually support heterogeneous hardware, failure recovery, efficient data movement, native execution, distributed memory, and large-scale workload management.

---

# License

See [LICENSE](LICENSE).

---

# Nodren

**Distributed computing infrastructure built around Go, Rust, C, C++, x86-64 Assembly, and systems programming.**

Repository:

https://github.com/MiroXdev0/Nodren
