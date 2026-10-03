# Nodren Core — C layer

The C layer is intentionally small and low-level.

It provides:
- raw virtual-memory backed allocations
- aligned allocations
- bump arenas
- atomic memory statistics
- the stable C ABI used by higher layers

The hot execution path does not allocate per task.

In the Windows release, this C memory layer is built into `norden-core.dll`.
The Worker is `norden-worker.exe`; the other release files are `Norden.exe`
(desktop app plus managed Go Controller) and `norden.exe` (Rust CLI).
