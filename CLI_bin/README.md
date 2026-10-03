# Nodren CLI

The supported Windows release CLI is `norden.exe`, built from
`CLI/Rust`. Add the Windows release directory (the directory containing
`Norden.exe`, `norden.exe`, `norden-worker.exe`, and `norden-core.dll`) to
`PATH`, then run commands such as:

```text
norden --help
norden status
norden workers list
```

`CLI_bin` is not the authoritative release output directory. Build and validate
the complete four-file Windows release from the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\test-clean-release.ps1
```

`Norden.exe` is the Avalonia desktop application and manages the Go Controller
internally. The CLI connects to its HTTP API by default; set
`NODREN_CONTROLLER_URL` or `NODREN_HTTP_ADDR` to target another Controller.
