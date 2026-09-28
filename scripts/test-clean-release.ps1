$ErrorActionPreference = "Stop"

function Get-FreePort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    return $port
}

function Wait-Until([scriptblock]$Condition, [string]$Description, [int]$Seconds = 15) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            if (& $Condition) { return }
        }
        catch {
        }
        Start-Sleep -Milliseconds 200
    }
    throw "Timed out waiting for $Description"
}

$root = Split-Path -Parent $PSScriptRoot
$sourceRelease = Join-Path $root "release"
if (-not (Test-Path -LiteralPath (Join-Path $sourceRelease "nodren.exe"))) {
    throw "Run build-release.ps1 before this test"
}
foreach ($artifact in @('nodren-worker.exe', 'nodren-ui.exe', 'nodren_core.dll')) {
    if (-not (Test-Path -LiteralPath (Join-Path $sourceRelease $artifact))) {
        throw "Release artifact is missing: $artifact"
    }
}

$clean = Join-Path ([System.IO.Path]::GetTempPath()) ("nodren-clean-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $clean | Out-Null
Copy-Item -Path (Join-Path $sourceRelease "*") -Destination $clean -Force

$controllerProcess = $null
$workerProcess = $null
$uiProcess = $null
try {
    $nodePort = Get-FreePort
    $httpPort = Get-FreePort
    $nodeAddress = "127.0.0.1:$nodePort"
    $httpAddress = "http://127.0.0.1:$httpPort"
    $env:NODREN_NODE_ADDR = $nodeAddress
    $env:NODREN_HTTP_ADDR = "127.0.0.1:$httpPort"
    $env:NODREN_CONTROLLER_URL = $httpAddress

    $controllerProcess = Start-Process -FilePath (Join-Path $clean "nodren.exe") -WorkingDirectory $clean -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $clean "controller.out") -RedirectStandardError (Join-Path $clean "controller.err")
    Wait-Until { (Invoke-RestMethod "$httpAddress/health").status -eq "ok" } "clean Controller health"

    $workerProcess = Start-Process -FilePath (Join-Path $clean "nodren-worker.exe") -WorkingDirectory $clean -PassThru -WindowStyle Hidden -ArgumentList @("--controller", $nodeAddress, "--id", "CLEAN-WORKER", "--cpu-cores", "2", "--ram-gb", "4") -RedirectStandardOutput (Join-Path $clean "worker.out") -RedirectStandardError (Join-Path $clean "worker.err")
    Wait-Until {
        $nodes = @(Invoke-RestMethod "$httpAddress/v1/nodes")
        $nodes.Count -eq 1 -and $nodes[0].state -eq "READY"
    } "clean worker registration"

    $result = (& (Join-Path $clean "nodren.exe") run sum 1 2 3 4 5 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $result -notmatch "Result\s+15") {
        throw "Clean release workload failed: $result"
    }

    $uiSelfTest = (& (Join-Path $clean "nodren-ui.exe") --self-test 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $uiSelfTest -notmatch "PASS: UI API self-test") {
        throw "Clean release UI self-test failed: $uiSelfTest"
    }

    $uiProcess = Start-Process -FilePath (Join-Path $clean "nodren-ui.exe") -WorkingDirectory $clean -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $clean "ui.out") -RedirectStandardError (Join-Path $clean "ui.err")
    Start-Sleep -Seconds 2
    if ($uiProcess.HasExited) {
        $uiError = if (Test-Path -LiteralPath (Join-Path $clean "ui.err")) { Get-Content -Raw (Join-Path $clean "ui.err") } else { "" }
        throw "Clean release UI did not stay running: $uiError"
    }

    Write-Host "PASS: clean release directory, Controller, worker, native Core, sum=15, UI self-test, and UI launch"
}
finally {
    if ($null -ne $uiProcess -and -not $uiProcess.HasExited) {
        Stop-Process -Id $uiProcess.Id -Force
    }
    if ($null -ne $workerProcess -and -not $workerProcess.HasExited) {
        Stop-Process -Id $workerProcess.Id -Force
    }
    if ($null -ne $controllerProcess -and -not $controllerProcess.HasExited) {
        Stop-Process -Id $controllerProcess.Id -Force
    }
    Remove-Item -LiteralPath $clean -Recurse -Force -ErrorAction SilentlyContinue
}
