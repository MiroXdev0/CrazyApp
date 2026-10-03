param(
    [string]$SourceRelease = ""
)

$ErrorActionPreference = "Stop"

function Wait-Until([scriptblock]$Condition, [string]$Description, [int]$Seconds = 20) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if (& $Condition) { return }
        Start-Sleep -Milliseconds 200
    }
    throw "Timed out waiting for $Description"
}

function Submit-Task([string]$BaseUrl, [hashtable]$Task) {
    return Invoke-RestMethod -Method Post -Uri "$BaseUrl/v1/tasks" -ContentType "application/json" -Body (@{ task = $Task } | ConvertTo-Json -Depth 10)
}

function Wait-Task([string]$BaseUrl, [string]$TaskId, [int]$Seconds = 20) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $state = Invoke-RestMethod "$BaseUrl/v1/tasks/$TaskId"
            if ($state.status -in @("COMPLETED", "FAILED", "CANCELLED", "TIMED_OUT")) {
                return $state
            }
        }
        catch [System.Net.WebException] {
        }
        catch [System.Net.Http.HttpRequestException] {
        }
        catch [System.Threading.Tasks.TaskCanceledException] {
        }
        catch {
            if ($_.Exception.GetType().FullName -ne "Microsoft.PowerShell.Commands.HttpResponseException") {
                throw
            }
        }
        Start-Sleep -Milliseconds 200
    }
    throw "Timed out waiting for task $TaskId"
}

function Task-Output([object]$Task) {
    if ([string]::IsNullOrWhiteSpace($Task.execution_result.stdout_base64)) {
        return ""
    }
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($Task.execution_result.stdout_base64)).Trim()
}

$root = Split-Path -Parent $PSScriptRoot
$requestedSourceRelease = $SourceRelease
$sourceRelease = if ([string]::IsNullOrWhiteSpace($requestedSourceRelease)) { Join-Path $root "Nodren-1.0.0-windows-x64" } else { (Resolve-Path -LiteralPath $requestedSourceRelease).Path }
$runtimeArtifacts = @('NodrenApp.exe', 'nodren.exe', 'nodren-worker.exe', 'nodren-core.dll')
$installerArtifacts = @('Install-Nodren.exe', 'Install-Nodren-Worker.exe')
$expectedRuntimeArtifacts = $runtimeArtifacts | Sort-Object
$expectedInstallerArtifacts = ($runtimeArtifacts + $installerArtifacts) | Sort-Object
$actualArtifacts = @(Get-ChildItem -LiteralPath $sourceRelease -File | Select-Object -ExpandProperty Name | Sort-Object)
$isPortableRelease = @(Compare-Object -ReferenceObject $expectedRuntimeArtifacts -DifferenceObject $actualArtifacts -CaseSensitive).Count -eq 0
$isInstallerRelease = @(Compare-Object -ReferenceObject $expectedInstallerArtifacts -DifferenceObject $actualArtifacts -CaseSensitive).Count -eq 0
$releaseItems = @(Get-ChildItem -LiteralPath $sourceRelease -Force)
if ((-not $isPortableRelease -and -not $isInstallerRelease) -or $releaseItems.Count -ne $actualArtifacts.Count) {
    throw "Release at '$sourceRelease' must contain exactly the four portable files, optionally with both installers. Found: $($actualArtifacts -join ', ')"
}

$clean = Join-Path ([System.IO.Path]::GetTempPath()) ("nodren-clean-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $clean | Out-Null
foreach ($name in $runtimeArtifacts) {
    Copy-Item -LiteralPath (Join-Path $sourceRelease $name) -Destination $clean -Force
}

$appProcess = $null
$duplicateProcess = $null
$workerProcess = $null
$previousPath = $env:PATH
$envNames = @('NODREN_CONTROLLER_URL', 'NODREN_HTTP_ADDR', 'NODREN_NODE_ADDR', 'NODREN_CORE_LIBRARY', 'NODREN_MANAGED_CONTROLLER_URL', 'NODREN_STATE_FILE', 'NODREN_AUTH_MODE', 'NODREN_API_TOKEN', 'NODREN_WORKER_TOKEN', 'NODREN_WORKER_ID', 'NODREN_WORKER_TOKENS', 'NODREN_HTTP_CERT_FILE', 'NODREN_HTTP_KEY_FILE', 'NODREN_SHUTDOWN_TOKEN')
$previousEnvironment = @{}
foreach ($name in $envNames) {
    $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
try {
    foreach ($name in $envNames) {
        Remove-Item "Env:$name" -ErrorAction SilentlyContinue
    }
    $env:NODREN_AUTH_MODE = "development"
    $env:NODREN_STATE_FILE = Join-Path $clean "controller-state.json"
    $env:PATH = "$clean;$previousPath"

    $cli = Join-Path $clean "nodren.exe"
    $help = (& nodren --help 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $help -notmatch "Usage:") {
        throw "nodren --help failed: $help"
    }

    $portProbe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 8080)
    try {
        try {
            $portProbe.Start()
        }
        catch [System.Net.Sockets.SocketException] {
            throw "Port 8080 is already in use; clean-release test requires an unused local Controller endpoint"
        }
    }
    finally {
        $portProbe.Stop()
    }

    $appProcess = Start-Process -FilePath (Join-Path $clean "NodrenApp.exe") -WorkingDirectory $clean -PassThru
    Wait-Until {
        if ($appProcess.HasExited) {
            throw "NodrenApp.exe exited during startup with code $($appProcess.ExitCode)"
        }
        try {
            $health = Invoke-RestMethod "http://127.0.0.1:8080/health" -TimeoutSec 1
            $health.status -eq "ok" -and $health.service -eq "nodren-controller"
        }
        catch [System.Net.WebException] {
            $false
        }
        catch [System.Net.Http.HttpRequestException] {
            $false
        }
        catch [System.Threading.Tasks.TaskCanceledException] {
            $false
        }
        catch {
            if ($_.Exception.GetType().FullName -eq "Microsoft.PowerShell.Commands.HttpResponseException") {
                $false
            }
            else {
                throw
            }
        }
    } "NodrenApp.exe managed Controller startup"
    Wait-Until {
        $appProcess.Refresh()
        $appProcess.MainWindowHandle -ne [IntPtr]::Zero
    } "NodrenApp.exe desktop window"

    $duplicateProcess = Start-Process -FilePath (Join-Path $clean "NodrenApp.exe") -WorkingDirectory $clean -PassThru
    if (-not $duplicateProcess.WaitForExit(5000)) {
        Stop-Process -Id $duplicateProcess.Id -Force
        throw "A second NodrenApp.exe instance stayed running instead of reusing the existing application"
    }
    $managedControllerPath = Join-Path $env:LOCALAPPDATA "Nodren\controller.exe"
    $managedControllers = @(Get-CimInstance Win32_Process -Filter "Name='controller.exe'" |
        Where-Object { $_.ExecutablePath -ieq $managedControllerPath })
    if ($managedControllers.Count -ne 1) {
        throw "Expected one managed Controller process after duplicate NodrenApp.exe launch; found $($managedControllers.Count)"
    }

    $workerHelp = (& (Join-Path $clean "nodren-worker.exe") --help 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $workerHelp -notmatch "controller") {
        throw "nodren-worker --help failed: $workerHelp"
    }

    $env:NODREN_CONTROLLER_URL = "http://127.0.0.1:8080"
    $env:NODREN_CORE_LIBRARY = $null
    $workerLog = Join-Path $clean "worker.log"
    $workerProcess = Start-Process -FilePath (Join-Path $clean "nodren-worker.exe") `
        -WorkingDirectory $clean -PassThru -WindowStyle Hidden `
        -ArgumentList @("--controller", "127.0.0.1:9000", "--id", "CLEAN-WORKER", "--cpu-cores", "2", "--ram-gb", "4") `
        -RedirectStandardOutput $workerLog -RedirectStandardError (Join-Path $clean "worker.err")
    Wait-Until {
        $nodes = @(Invoke-RestMethod "http://127.0.0.1:8080/v1/nodes")
        $nodes.Count -eq 1 -and $nodes[0].state -eq "READY"
    } "worker connection and native Core load"

    $runOutput = (& $cli run sum 1 2 3 4 5 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $runOutput -notmatch "Result\s+15") {
        throw "nodren run failed: $runOutput"
    }

    $statusOutput = (& $cli status 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $statusOutput -notmatch "Controller\s+ONLINE") {
        throw "nodren status failed: $statusOutput"
    }

    $requirements = @{ cpu_cores = 1; ram_gb = 1; gpu_required = $false }
    $task = Submit-Task "http://127.0.0.1:8080" @{
        type = "PROCESS"; version = "1"; executable = "cmd.exe"; arguments = @("/C", "echo release-ok")
        requirements = $requirements; stdout_limit_bytes = 4096; stderr_limit_bytes = 4096
    }
    $taskResult = Wait-Task "http://127.0.0.1:8080" $task.id
    if ($taskResult.status -ne "COMPLETED" -or (Task-Output $taskResult) -ne "release-ok") {
        throw "Clean-release process workload failed: $($taskResult | ConvertTo-Json -Depth 10)"
    }

    $shutdownProcess = Start-Process -FilePath (Join-Path $clean "NodrenApp.exe") -WorkingDirectory $clean -ArgumentList @("--shutdown") -PassThru
    $shutdownRequestExited = $shutdownProcess.WaitForExit(5000)
    if (-not $shutdownRequestExited) {
        Stop-Process -Id $shutdownProcess.Id -Force
        $shutdownProcess.WaitForExit(5000) | Out-Null
    }
    if (-not $shutdownRequestExited -or $shutdownProcess.ExitCode -ne 0 -or -not $appProcess.WaitForExit(15000)) {
        throw "NodrenApp.exe did not close through its normal application shutdown path"
    }
    Wait-Until {
        try {
            Invoke-RestMethod "http://127.0.0.1:8080/health" -TimeoutSec 1 | Out-Null
            $false
        }
        catch [System.Net.WebException] {
            $true
        }
        catch [System.Net.Http.HttpRequestException] {
            $true
        }
        catch [System.Threading.Tasks.TaskCanceledException] {
            $true
        }
        catch {
            if ($_.Exception.GetType().FullName -eq "Microsoft.PowerShell.Commands.HttpResponseException") {
                $true
            }
            else {
                throw
            }
        }
    } "managed Controller shutdown"

    Write-Host "PASS: clean release contents, nodren --help/status/run, NodrenApp.exe managed startup/shutdown, worker --help/registration/Core loading, and workload execution"
}
finally {
    if ($null -ne $workerProcess -and -not $workerProcess.HasExited) {
        Stop-Process -Id $workerProcess.Id -Force
    }
    if ($null -ne $duplicateProcess -and -not $duplicateProcess.HasExited) {
        Stop-Process -Id $duplicateProcess.Id -Force
        $duplicateProcess.WaitForExit(5000) | Out-Null
    }
    if ($null -ne $appProcess -and -not $appProcess.HasExited) {
        try {
            $shutdownProcess = Start-Process -FilePath (Join-Path $clean "NodrenApp.exe") `
                -WorkingDirectory $clean -ArgumentList @("--shutdown") -PassThru
            if ($shutdownProcess.WaitForExit(5000) -and $shutdownProcess.ExitCode -eq 0) {
                $appProcess.WaitForExit(10000)
            } elseif (-not $shutdownProcess.HasExited) {
                Stop-Process -Id $shutdownProcess.Id -Force
                $shutdownProcess.WaitForExit(5000) | Out-Null
            }
        }
        catch [System.ComponentModel.Win32Exception] {
            Write-Warning "Could not start the shutdown request process; the test-owned process tree will be terminated."
        }
        if (-not $appProcess.HasExited) {
            Stop-Process -Id $appProcess.Id -Force
            $appProcess.WaitForExit(5000)
        }
    }
    foreach ($name in $envNames) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], "Process")
    }
    $env:PATH = $previousPath
    Remove-Item -LiteralPath $clean -Recurse -Force
}
