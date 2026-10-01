# Nodren Core

Native execution library for Nodren.

Nodren Core provides the low-level native interface used by workloads that require native computation. It is designed to sit below the Rust worker runtime and provide efficient CPU-side execution without putting high-level orchestration logic into the native layer.

```text
                    Nodren Controller
                           │
                           │ workload / task
                           ▼
                    Rust Worker Runtime
                           │
                           │ native workload
                           ▼
                    ┌───────────────┐
                    │  Nodren Core  │
                    ├───────────────┤
                    │ C ABI         │
                    │ C / C++       │
                    │ Memory        │
                    │ Dispatch      │
                    │ Native Tasks  │
                    └───────┬───────┘
                            │
                            ▼
                       x86-64 CPU
                            │
                       AVX2 / SIMD
                       where useful
```

The Core is deliberately independent from the Go Controller, Rust worker implementation, Python tooling, and desktop UI.

---

## Design goals

Nodren Core is designed around a small and explicit native boundary.

* explicit C ABI
* fixed-size/POD task descriptors where appropriate
* bounded execution structures
* native memory management
* aligned allocations where required
* CPU feature detection and dispatch
* low-overhead native execution
* minimal abstraction overhead on hot paths
* no JSON in the native compute path
* no dependency on the Controller
* no dependency on the desktop UI
* deterministic correctness tests before optimization
* assembly only where profiling justifies it

The Core should remain a focused native component rather than becoming a second scheduler or controller.

---

## What the Core does

The Core is responsible for native computation that has an explicit native workload implementation.

Examples currently include:

```text
sum
xor
dot_product
```

A workload can enter the native layer through the C ABI and be executed using the available native implementation.

The Core may provide:

```text
task validation
native memory operations
CPU dispatch
optimized kernels
workload execution
result generation
```

The Rust worker remains responsible for higher-level execution concerns such as:

```text
Controller communication
task reception
artifact staging
resource validation
process execution
script execution
timeouts
cancellation
result transmission
worker lifecycle
```

This separation is intentional.

---

## What the Core does not do

Nodren Core is **not** the distributed scheduler.

It does not:

* select workers
* communicate with the Controller
* manage the cluster
* transfer artifacts
* decide workload distribution
* manage network connections
* provide the HTTP API
* provide the desktop UI
* automatically distribute arbitrary executables

Distributed execution is coordinated by the Controller and worker runtime.

The Core executes native workloads after they have been assigned to a worker.

---

## Language layers

The native implementation is divided according to responsibility.

```text
C
│
├── low-level memory interfaces
├── allocation primitives
├── ABI boundaries
└── C-compatible public interfaces
        │
        ▼
C++
│
├── native workload implementation
├── execution logic
├── dispatch
└── performance-sensitive native code
        │
        ▼
x86-64 Assembly
│
└── selected low-level kernels
```

Assembly is not used simply because it is available.

The preferred implementation is ordinary C/C++ unless profiling shows that a specific operation benefits from a specialized implementation.

---

## C ABI

The Core exposes a stable C-compatible interface so that higher-level components do not need to depend directly on the C++ ABI.

Conceptually:

```text
Rust Worker
     │
     │ FFI
     ▼
┌───────────────┐
│    C ABI      │
├───────────────┤
│ task input    │
│ task metadata │
│ execution     │
│ task result   │
└───────┬───────┘
        ▼
     C / C++
```

This keeps the boundary explicit and makes the native library easier to consume from different runtimes.

---

## CPU dispatch

The Core can detect relevant CPU capabilities and select an appropriate implementation where multiple implementations exist.

Conceptually:

```text
                 Native workload
                       │
                       ▼
                CPU feature check
                       │
             ┌─────────┴─────────┐
             ▼                   ▼
        Generic path        SIMD path
                                 │
                              AVX2
```

The generic implementation should remain available when a specialized instruction set is unavailable.

CPU-specific optimization must not compromise correctness or portability within the supported architecture.

---

## Memory

Native workloads may require memory with properties that are different from ordinary application allocations.

The Core therefore provides native memory functionality where required by its execution model.

Design priorities include:

* explicit ownership
* predictable allocation behavior
* alignment
* bounded memory usage
* avoiding unnecessary copies
* clear lifetime rules

Memory management should remain explicit rather than hidden behind unnecessary abstractions.

---

## Execution model

The Core is intended for low-overhead native execution.

The high-level path is approximately:

```text
Controller
    │
    ▼
Rust Worker
    │
    ├── validate workload
    ├── stage required artifacts
    └── prepare native task
            │
            ▼
        C ABI
            │
            ▼
       Nodren Core
            │
            ├── validate
            ├── dispatch
            └── execute
            │
            ▼
          result
            │
            ▼
        Rust Worker
            │
            ▼
        Controller
```

The Core does not create a new operating-system process for every native task.

Native workloads execute inside the worker process through the native interface.

---

## Correctness before optimization

Native code is particularly sensitive to undefined behavior, memory errors, integer overflow, alignment mistakes, and incorrect SIMD implementations.

Nodren Core therefore follows:

```text
Implementation
      ↓
Correctness tests
      ↓
Benchmark
      ↓
Profile
      ↓
Optimize
      ↓
Re-test
```

An optimized implementation that produces incorrect results is not considered an optimization.

---

## Build

### Windows

From the `Core` directory:

```powershell
.\build_core.ps1
```

The Windows native library is:

```text
build/
└── nodren-core.dll
```

The exact build requirements depend on the selected compiler/toolchain.

---

### Linux x64

From the `Core` directory:

```bash
bash ./build_core.sh
```

The Linux build produces:

```text
build/
└── libnodren_core.so
```

along with native self-test executables.

Linux x64 support is currently **Beta / Unstable**.

A Linux x86-64 compiler/toolchain is required.

The Linux release layout uses:

```text
release/LinuxX64/

├── nodren-worker
└── libnodren_core.so
```

---

## Windows release

The native Core is distributed as part of the four-file Windows x64 Nodren release:

```text
release/

├── nodren.exe
├── nodren.exe-CLI
├── nodren-worker.exe
└── nodren-core.dll
```

`nodren-core.dll` is consumed by `nodren-worker.exe` when a workload requires the native execution layer.

The Core is not a standalone distributed-computing controller.

---

## Testing

Core testing should cover both correctness and native integration.

Important test categories include:

```text
C ABI tests
Native workload tests
Memory tests
Boundary-condition tests
Overflow tests
CPU dispatch tests
SIMD correctness tests
Self-tests
Worker/Core integration tests
```

Correctness is tested independently of performance.

Performance benchmarks should be used to determine whether an optimization is actually beneficial.

---

## Relationship with Nodren

The Core is one layer of the larger Nodren system:

```text
┌──────────────────────────────┐
│          Nodren CLI          │
└──────────────┬───────────────┘
               │
               ▼
┌──────────────────────────────┐
│       Go Controller          │
│ Scheduler / Jobs / Resources │
└──────────────┬───────────────┘
               │
             TCP
               │
               ▼
┌──────────────────────────────┐
│        Rust Worker           │
│ Execution / Artifacts / FFI  │
└──────────────┬───────────────┘
               │
              FFI
               │
               ▼
┌──────────────────────────────┐
│         Nodren Core          │
│       C / C++ / ASM          │
└──────────────────────────────┘
```

This separation allows Nodren to support workloads that do not require the native Core while still providing a low-level execution path for workloads that do.

---

## Status

Nodren Core is an active part of the Nodren pre-release architecture.

Current native workload examples:

```text
sum
xor
dot_product
```

The native interface is intended to expand as Nodren gains additional workload adapters and execution capabilities.

The Core should remain small, predictable, and focused on native execution rather than absorbing responsibilities belonging to the Controller or worker runtime.

---

**Nodren Core**

> Native execution where the workload needs it.
