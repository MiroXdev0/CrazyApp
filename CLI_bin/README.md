# Nodren CLI

The supported Windows release CLI is `nodren.exe`, built from
`CLI/Rust`. Add the Windows release directory (the directory containing
`NodrenApp.exe`, `nodren.exe`, `nodren-worker.exe`, and `nodren-core.dll`) to
`PATH`, then run commands such as:

```text
nodren --help
nodren status
nodren workers list
```

`CLI_bin` is not the authoritative release output directory. Build and validate
the complete four-file Windows release from the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-release.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\test-clean-release.ps1
```

`NodrenApp.exe` is the Avalonia desktop application and manages the Go Controller
internally. The CLI connects to its HTTP API by default; set
`NODREN_CONTROLLER_URL` or `NODREN_HTTP_ADDR` to target another Controller.
When connecting to a secure Controller over HTTPS, also set
`NODREN_API_TOKEN` to its bearer credential.
