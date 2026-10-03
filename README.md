# Nodren

**Distributed compute infrastructure for turning multiple machines into one execution platform.**

Nodren connects computers into a distributed execution cluster and schedules workloads across available resources.

## ✨ Features

* Distributed workload execution
* CPU & GPU resource-aware scheduling
* Real ONNX and GGUF/llama.cpp AI execution
* Live worker and cluster telemetry
* Automatic worker recovery
* Native C/C++ execution core
* CLI and desktop applications
* Artifact transfer and integrity verification

## 🏗️ Architecture

```text
CLI / Desktop / Web
        │
        ▼
 Go Controller
        │
   TCP Protocol
        │
   ┌────┼────┐
   ▼    ▼    ▼
Worker Worker Worker
   │    │    │
   └────┼────┘
        ▼
 Native Core
```

## 🚀 Quick Start

### Desktop installation

Download and run `Install-Nodren.exe` from the Nodren 1.0.1 Windows x64
release. By default it installs the desktop application, CLI, Worker, and
native Core under `%ProgramFiles%\Nodren`; it also allows a per-user install
when administrator rights are unavailable. The installer offers optional
desktop shortcut and PATH tasks. The desktop application starts and
manages its local Controller; no separate Controller executable or service is
installed.

Launch Nodren from the Start Menu, or open a new terminal for CLI access:

```powershell
nodren --help
nodren status
```

### Worker installation

For a separate worker machine, run `Install-Nodren-Worker.exe`. It installs
only `nodren-worker.exe`, `nodren-core.dll`, and the Worker configuration
instructions under `%ProgramFiles%\Nodren\Worker`. It does not start a service,
open firewall ports, or start the worker automatically.

Configure connection settings using the existing command-line and environment
configuration. In PowerShell:

```powershell
# For a per-user install, change this to the selected Worker install directory.
Set-Location "$env:ProgramFiles\Nodren\Worker"
$secureToken = Read-Host "Worker token" -AsSecureString
$env:NODREN_WORKER_TOKEN = [System.Net.NetworkCredential]::new("", $secureToken).Password
Remove-Variable secureToken
.\nodren-worker.exe --controller <controller-host>:9000 --id <worker-id>
```

Supply the Worker ID and matching unique token configured by the Controller
administrator. The token is held in the current PowerShell session. The
Worker TCP protocol authenticates and integrity-protects traffic but does not
encrypt it; use a trusted private network or encrypted tunnel.

### Uninstall and upgrades

Each installer registers its own Windows uninstaller. Upgrading with the same
installer preserves existing application settings and Controller state under
`%AppData%\Nodren` and `%LocalAppData%\Nodren`. Uninstall removes the installed
program files and shortcuts, but preserves those user-data locations. The
desktop install can optionally add its CLI directory to the system PATH; its
uninstaller removes only the PATH entry it added.

## 🧰 Build Windows installers

From a Windows development machine with the Nodren release build prerequisites:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-installers.ps1
```

To exercise install, upgrade, uninstall, and reinstall flows using isolated
per-user test installations:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\test-installers.ps1
```

The script rebuilds and validates the four portable release binaries, then
builds the standalone installers using pinned Inno Setup 6.7.3 (downloaded
from its official release URL and SHA-256 verified if no compiler is present).
Output is written to `Nodren-1.0.1-windows-x64`, containing the four portable
files plus `Install-Nodren.exe` and `Install-Nodren-Worker.exe`.

## 🤖 AI

Nodren supports real AI execution through:

* ONNX Runtime
* GGUF / llama.cpp
* CPU and GPU execution
* Resource-aware scheduling
* Model inspection
* Streaming output

## 📦 Release

**Latest:** `V1.0.1-Windows(x86_64)`

Windows x86_64 is currently the primary supported platform. Linux support is under development.

## 🛠️ Built With

**Go · Rust · C · C++ · Assembly · TypeScript · C#**

## 📖 Documentation

See the repository documentation for architecture, development, configuration, and advanced usage.

## 📄 License

See [LICENSE](LICENSE).
