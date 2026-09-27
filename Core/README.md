# Nodren Core

Native high-performance execution core for Nodren.

```text
C
  raw memory / arenas / OS virtual memory
      ↓
C++
  task queue / workers / execution engine / dispatch
      ↓
x86-64 ASM
  AVX2 hot kernels
```

The core is deliberately independent from Go, Rust, Python, and the UI.

## Design goals

- no per-task process creation
- no JSON in the compute path
- bounded task queues
- fixed POD task descriptors
- native aligned memory
- explicit C ABI
- CPU feature dispatch
- assembly only where profiling justifies it
- deterministic correctness tests before optimization

## Build

Windows PowerShell:

```powershell
./build_core.ps1
```

Linux:

```bash
./build_core.sh
```
