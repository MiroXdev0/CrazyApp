# Nodren Core — C++

The C++ layer owns:
- native task execution
- bounded lock-free task transport
- worker threads
- CPU feature dispatch
- C ABI integration

The hot path uses POD task descriptors and avoids `std::function`, JSON,
or per-task heap allocation.

In the Windows release, this source contributes to `norden-core.dll`, which
`norden-worker.exe` loads through the C ABI. The complete release also
contains `Norden.exe` (the Avalonia app plus managed Go Controller) and
`norden.exe` (the Rust CLI).
