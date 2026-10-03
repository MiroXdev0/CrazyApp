Nodren Worker 1.0.1
====================

The worker connects only when you launch it. This installer does not create a
service, open firewall ports, or start the worker automatically.

To connect to a Controller, open PowerShell in the selected Worker installation
directory and run:

  $secureToken = Read-Host "Worker token" -AsSecureString
  $env:NODREN_WORKER_TOKEN = [System.Net.NetworkCredential]::new("", $secureToken).Password
  Remove-Variable secureToken
  .\nodren-worker.exe --controller <controller-host>:9000 --id <worker-id>

Use the unique token configured for this worker ID in the Controller's
NODREN_WORKER_TOKENS setting. Secure mode requires a token of at least 32
characters. The token is held only in this PowerShell process and passed to
the worker through the existing NODREN_WORKER_TOKEN environment variable.
The Controller address and worker ID above are examples/placeholders; replace
them with values supplied by your Nodren administrator. Do not use a public
or untrusted network for the current TCP protocol: it authenticates and
integrity-protects frames but does not encrypt traffic.

The worker supports NODREN_CONTROLLER_ADDR and NODREN_WORKER_ID as existing
environment-based fallbacks. Its command-line --controller and --id options
take precedence. See `nodren-worker.exe --help` for all supported options.

Uninstalling the worker removes only the installed program files and shortcuts.
It does not remove user or machine environment variables or any files outside
this installation directory.
