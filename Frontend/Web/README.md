# Nodren Web Frontend

This directory contains the TypeScript/Bun web frontend. `index.ts` loads the
browser UI, and `src/` contains the dashboard and client modules. The current
web UI is separate from the Windows desktop application; it is not one of the
Windows release executables.

Build the frontend bundle from this directory:

```bash
bun install
bun run build
```

The package build command writes the browser bundle to `dist/`. The
`ConnectToServer` module requests `/api/status` from a gateway; the static
frontend does not itself implement the Go Controller API gateway. The
`nexus-client` sample currently returns placeholder worker data and should
not be treated as live cluster telemetry.

The current Windows release consists of exactly:

```text
NodrenApp.exe
nodren.exe
nodren-worker.exe
nodren-core.dll
```

`NodrenApp.exe` is the Avalonia desktop app and manages its Go Controller.
`nodren.exe` is the Rust CLI, `nodren-worker.exe` is the Rust Worker, and
`nodren-core.dll` is the Worker native Core loaded through the C ABI. The
frontend sources are not extra release executables.
