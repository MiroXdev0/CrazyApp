# Nodren Core — x86-64 Assembly

Assembly is used only for measured CPU hot paths.

`sum_avx2.S` provides a vectorized signed-32-bit to signed-64-bit reduction.
Windows and Linux use their native x86-64 calling conventions.

The C++ runtime dispatches to this kernel only when AVX2 is available.

The Windows release builds this kernel into `nodren-core.dll`, loaded by
`nodren-worker.exe`. The four Windows release files are `NodrenApp.exe`
(desktop app plus managed Go Controller), `nodren.exe` (Rust CLI),
`nodren-worker.exe`, and `nodren-core.dll`.
