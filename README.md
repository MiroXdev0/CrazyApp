# Nodren

**Distributed computing infrastructure for turning multiple machines into one computational system.**

Nodren is a systems project focused on distributed execution, high-performance communication, native computation, resource management, and fault-tolerant workloads.

The goal is to let a controller coordinate ordinary machines as a shared compute cluster while keeping the heavy computation close to the native runtime.

---

## Architecture

```text
                           Nodren
                              │
              ┌───────────────┴───────────────┐
              │                               │
         Control Plane                    Compute Plane
              │                               │
             Go                              C/C++
              │                               │
        ┌─────┴─────┐                 ┌───────┴───────┐
        │ Controller │                 │  Native Core  │
        └─────┬─────┘                 └───────┬───────┘
              │                               │
       Persistent transport             C ABI boundary
              │                               │
      ┌───────┼────────┐                C / C++ / ASM
      │       │        │
   Node 1   Node 2   Node N
      │       │        │
      └───────┼────────┘
              │
         Task execution
```

Nodren separates orchestration from computation:

**Controller**
Schedules work, tracks nodes, manages jobs, and coordinates execution.

**Node**
Represents a participating machine and manages communication, resources, task queues, and execution.

**Core**
Performs the computationally expensive work using native code.

---

## Core principles

### Distributed by design

Nodren is built around multiple independent machines rather than treating distribution as an afterthought.

### Native computation

Heavy workloads belong in the native runtime instead of the control layer.

### Persistent communication

Nodes maintain long-lived connections instead of repeatedly creating processes or connections for individual tasks.

### Batching

Tasks and results can be grouped together to reduce communication and scheduling overhead.

### Backpressure

Nodes must be able to signal when they are busy or saturated so the controller does not continuously overload them.

### Measured performance

Performance changes are benchmarked and profiled rather than optimized based on assumptions.

### Fault tolerance

A failed node should not automatically mean a failed workload. Tasks must be recoverable and reschedulable.

---

## Project structure

```text
Nodren/
│
├── Backend/
│   ├── Server/          # Controller and orchestration services
│   ├── Data/            # Data and telemetry services
│   └── API/             # External APIs
│
├── Core/
│   ├── C/               # Low-level runtime and memory primitives
│   ├── Cpp/             # Native execution and compute engine
│   └── Assembly/        # Architecture-specific operations
│
├── Systems/
│   ├── Go/              # Distributed control plane
│   └── Rust/            # Systems and safety-critical components
│
├── Shared/
│   ├── Protocol/        # Nodren protocol definitions
│   ├── IPC/             # Inter-process communication
│   ├── Serialization/   # Serialization infrastructure
│   └── C_API/           # Native ABI boundaries
│
├── Frontend/
│   ├── Desktop/         # Desktop management interface
│   └── Web/             # Web management interface
│
├── Python/
│   ├── Analytics/       # Benchmark and telemetry analysis
│   ├── AI/              # Experimental intelligent tooling
│   ├── Tools/           # Development utilities
│   └── Scripts/         # Automation
│
├── Database/
│   ├── SQL/             # Database definitions
│   ├── migrations/      # Schema migrations
│   └── schemas/         # Data schemas
│
├── Native/
│   ├── include/         # Native interfaces
│   ├── lib/             # Native libraries
│   └── bindings/        # Language bindings
│
├── Tests/
│   ├── Cpp/
│   ├── Rust/
│   ├── CSharp/
│   ├── Python/
│   └── Integration/
│
├── docs/                # Technical documentation
├── tools/               # Developer and benchmark tools
├── scripts/             # Build and automation scripts
│
├── .gitattributes
├── .gitignore
└── README.md
```

---

## Communication model

Nodren uses persistent sessions between the controller and nodes.

```text
Controller
    │
    │ REGISTER
    ▼
   Node
    │
    │ READY
    ▼
Controller
    │
    │ TASK_BATCH
    ▼
   Node
    │
    │ native execution
    ▼
   Core
    │
    │ TASK_RESULT_BATCH
    ▼
Controller
```

The protocol is designed around framed messages rather than repeatedly creating a new process or connection for every task.

Typical message types include:

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

Each task carries identifiers that allow results to be matched independently of execution order.

---

## High-performance runtime

The performance architecture is split between the control plane and compute plane.

### Go

Used for:

* controller services
* node communication
* persistent connections
* task dispatch
* batching
* backpressure
* scheduling infrastructure
* telemetry

### C++

Used for:

* task execution
* native worker pools
* compute-heavy algorithms
* memory-efficient data structures
* performance-critical paths

### C

Used for low-level native interfaces and memory/runtime primitives.

### Rust

Used where stronger memory and concurrency guarantees are useful at the systems layer.

### Assembly

Reserved for architecture-specific operations where low-level CPU instructions provide a measurable benefit.

### Python

Used for:

* automation
* analysis
* benchmark tooling
* experimentation
* development utilities

### C#

Used for desktop tooling and management interfaces.

---

## Performance engineering

Nodren contains dedicated benchmarks instead of relying on assumptions about performance.

The benchmark infrastructure measures things such as:

```text
total execution time
tasks per second
operations per second
queue wait
transport overhead
IPC overhead
native computation time
result collection
latency
CPU utilization
memory usage
scaling efficiency
```

Configurations can be compared using:

```text
1 node
2 nodes
4 nodes
8 nodes
```

and different native worker counts.

The benchmark is designed to keep workload size and computation identical between configurations so that scaling results are comparable.

---

## Error handling

Errors are treated as part of the system architecture rather than plain log messages.

The error subsystem is designed to carry information such as:

```text
error code
component
severity
message
node ID
job ID
task ID
timestamp
recoverability
```

Errors can originate from:

```text
networking
protocol
nodes
jobs
tasks
resources
memory
native execution
```

Recoverable failures can be retried or rescheduled while unrecoverable failures can terminate the affected task or job.

---

## Distributed execution

A future complete workload looks like:

```text
User
 │
 ▼
Controller
 │
 ├── split workload
 ├── inspect resources
 ├── schedule tasks
 └── distribute work
        │
        ├─────────────┬─────────────┐
        ▼             ▼             ▼
      Node 1        Node 2        Node 3
        │             │             │
      Core          Core          Core
        │             │             │
        └─────────────┼─────────────┘
                      ▼
                Result assembly
                      │
                      ▼
                    User
```

The long-term objective is for Nodren to handle the full lifecycle:

```text
submit
  ↓
queue
  ↓
split
  ↓
schedule
  ↓
execute
  ↓
collect
  ↓
reconstruct
  ↓
complete
```

while remaining able to recover from node and task failures.

---

## Current development state

Nodren is currently in active systems-development.

Implemented work includes:

* controller/node architecture
* node registration and communication
* native compute components
* structured error handling
* performance benchmarking
* persistent transport infrastructure
* framed binary transport
* batched result handling
* performance diagnostics
* same-machine scaling tests

Work still being developed includes:

* full distributed job scheduling
* production task batching
* resource-aware scheduling
* fault recovery and task rescheduling
* multi-machine execution
* runtime isolation
* advanced workload management
* production-grade observability

---

## Development philosophy

Nodren is intentionally built from multiple layers rather than one large runtime.

The architecture follows:

```text
Go
 ↓
Transport / orchestration
 ↓
C ABI
 ↓
C/C++
 ↓
Native execution
 ↓
CPU / memory
```

Every layer should have a defined responsibility.

Performance improvements should be supported by measurements.

Correctness should be tested before optimization.

Distributed behavior should be tested under failure, not only under ideal conditions.

---

## Building

Nodren is currently under active development, so build commands vary by subsystem.

Typical development tools include:

```bash
go test ./...
```

and native C++ builds using the project's configured compiler/toolchain.

Generated binaries and intermediate build artifacts should not be committed to the repository.

---

## License

See [LICENSE](LICENSE).

---

## Project

**Nodren**
Distributed computing infrastructure built around Go, C, C++, Rust, Python, and native systems programming.
