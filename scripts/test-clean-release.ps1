param(
    [string]$SourceRelease = "",
    [switch]$BasicOnly
)

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

function Submit-Task([string]$BaseUrl, [hashtable]$Task) {
    return Invoke-RestMethod -Method Post -Uri "$BaseUrl/v1/tasks" -ContentType "application/json" -Body (@{ task = $Task } | ConvertTo-Json -Depth 10)
}

function Wait-Task([string]$BaseUrl, [string]$TaskId, [string]$Description, [int]$Seconds = 20) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $state = Invoke-RestMethod "$BaseUrl/v1/tasks/$TaskId"
            if ($state.status -in @("COMPLETED", "FAILED", "CANCELLED", "TIMED_OUT")) {
                return $state
            }
        }
        catch {
        }
        Start-Sleep -Milliseconds 200
    }
    throw "Timed out waiting for $Description"
}

function Task-Output([object]$Task) {
    if ([string]::IsNullOrWhiteSpace($Task.execution_result.stdout_base64)) {
        return ""
    }
    return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($Task.execution_result.stdout_base64)).Trim()
}

$root = Split-Path -Parent $PSScriptRoot
$sourceRelease = if ([string]::IsNullOrWhiteSpace($SourceRelease)) { Join-Path $root "release" } else { (Resolve-Path -LiteralPath $SourceRelease).Path }
if (-not (Test-Path -LiteralPath (Join-Path $sourceRelease "nodren.exe"))) {
    throw "Run build-release.ps1 before this test"
}
foreach ($artifact in @('nodren.exe', 'nodren.exe-CLI', 'nodren-worker.exe', 'nodren-core.dll')) {
    if (-not (Test-Path -LiteralPath (Join-Path $sourceRelease $artifact))) {
        throw "Release artifact is missing: $artifact"
    }
}

$expectedArtifacts = @('nodren-core.dll', 'nodren-worker.exe', 'nodren.exe', 'nodren.exe-CLI') | Sort-Object
$actualArtifacts = @(Get-ChildItem -LiteralPath $sourceRelease -File | Select-Object -ExpandProperty Name | Sort-Object)
if (@(Compare-Object -ReferenceObject $expectedArtifacts -DifferenceObject $actualArtifacts).Count -ne 0) {
    throw "Release contains files other than the four required Windows artifacts"
}

$clean = Join-Path ([System.IO.Path]::GetTempPath()) ("nodren-clean-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $clean | Out-Null
Copy-Item -Path (Join-Path $sourceRelease "*") -Destination $clean -Force

$controllerProcess = $null
$workerProcess = $null
$previousCoreLibrary = $env:NODREN_CORE_LIBRARY
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

    $env:NODREN_CORE_LIBRARY = Join-Path $clean "nodren-core.dll"
    $workerProcess = Start-Process -FilePath (Join-Path $clean "nodren-worker.exe") -WorkingDirectory $clean -PassThru -WindowStyle Hidden -ArgumentList @("--controller", $nodeAddress, "--id", "CLEAN-WORKER", "--cpu-cores", "2", "--ram-gb", "4") -RedirectStandardOutput (Join-Path $clean "worker.out") -RedirectStandardError (Join-Path $clean "worker.err")
    Wait-Until {
        $nodes = @(Invoke-RestMethod "$httpAddress/v1/nodes")
        $nodes.Count -eq 1 -and $nodes[0].state -eq "READY"
    } "clean worker registration"

    $result = (& (Join-Path $clean "nodren.exe") run sum 1 2 3 4 5 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $result -notmatch "Result\s+15") {
        throw "Clean release workload failed: $result"
    }

    if ($BasicOnly) {
        $cliOutput = Join-Path $clean "cli-version.out"
        $cliError = Join-Path $clean "cli-version.err"
        $cliProcess = Start-Process -FilePath (Join-Path $clean "nodren.exe-CLI") -WorkingDirectory $clean -ArgumentList @("--version") -Wait -PassThru -WindowStyle Hidden -RedirectStandardOutput $cliOutput -RedirectStandardError $cliError
        $cliVersion = if (Test-Path -LiteralPath $cliOutput) { Get-Content -Raw -LiteralPath $cliOutput } else { "" }
        if ($cliProcess.ExitCode -ne 0 -or $cliVersion -notmatch "0\.2\.0") {
            throw "Clean release CLI failed: $cliVersion"
        }
        Write-Host "PASS: clean release directory, Controller, worker, native Core, sum=15, and separate CLI"
        return
    }

    $requirements = @{ cpu_cores = 1; ram_gb = 1; gpu_required = $false }
    $processTask = Submit-Task $httpAddress @{
        type = "PROCESS"; version = "1"; executable = "cmd.exe"; arguments = @("/C", "echo process-ok")
        requirements = $requirements; stdout_limit_bytes = 4096; stderr_limit_bytes = 4096
    }
    $processResult = Wait-Task $httpAddress $processTask.id "process task"
    if ($processResult.status -ne "COMPLETED" -or (Task-Output $processResult) -ne "process-ok") {
        throw "Process task failed: $($processResult | ConvertTo-Json -Depth 10)"
    }

    $commandTask = Submit-Task $httpAddress @{
        type = "COMMAND"; version = "1"; executable = "cmd.exe"; arguments = @("/C", "echo command-ok")
        requirements = $requirements; stdout_limit_bytes = 4096; stderr_limit_bytes = 4096
    }
    $commandResult = Wait-Task $httpAddress $commandTask.id "command task"
    if ($commandResult.status -ne "COMPLETED" -or (Task-Output $commandResult) -ne "command-ok") {
        throw "Command task failed: $($commandResult | ConvertTo-Json -Depth 10)"
    }

    Set-Content -LiteralPath (Join-Path $clean "smoke.py") -Value 'print("script-ok")' -Encoding utf8
    $scriptTask = Submit-Task $httpAddress @{
        type = "SCRIPT"; version = "1"; runtime = "python"; script = "smoke.py"
        requirements = $requirements; stdout_limit_bytes = 4096; stderr_limit_bytes = 4096
    }
    $scriptResult = Wait-Task $httpAddress $scriptTask.id "script task"
    if ($scriptResult.status -ne "COMPLETED" -or (Task-Output $scriptResult) -ne "script-ok") {
        throw "Script task failed: $($scriptResult | ConvertTo-Json -Depth 10)"
    }

    $artifactPath = Join-Path $clean "artifact.txt"
    [IO.File]::WriteAllBytes($artifactPath, [Text.Encoding]::UTF8.GetBytes("artifact-ok`r`n"))
    $artifactHash = (Get-FileHash -LiteralPath $artifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $artifactResponse = Invoke-WebRequest -Method Post -Uri "$httpAddress/v1/artifacts?name=artifact.txt" -InFile $artifactPath -ContentType "application/octet-stream" -Headers @{
        "X-Nodren-Artifact-Name" = "artifact.txt"; "X-Nodren-Artifact-Kind" = "input"; "X-Nodren-Artifact-SHA256" = $artifactHash
    }
    $artifact = $artifactResponse.Content | ConvertFrom-Json
    $artifactTask = Submit-Task $httpAddress @{
        type = "PROCESS"; version = "1"; executable = "cmd.exe"; arguments = @("/C", "type artifact.txt")
        input_artifacts = @(@{ id = $artifact.id; name = "artifact.txt"; size = [uint64]$artifact.size; sha256 = $artifact.sha256; kind = "input" })
        requirements = $requirements; stdout_limit_bytes = 4096; stderr_limit_bytes = 4096
    }
    $artifactResult = Wait-Task $httpAddress $artifactTask.id "artifact task"
    if ($artifactResult.status -ne "COMPLETED" -or (Task-Output $artifactResult) -ne "artifact-ok") {
        throw "Artifact task failed: $($artifactResult | ConvertTo-Json -Depth 10)"
    }

    $longArguments = @("/C", "ping -n 15 127.0.0.1 > NUL")
    $cancelTask = Submit-Task $httpAddress @{
        type = "PROCESS"; version = "1"; executable = "cmd.exe"; arguments = $longArguments
        requirements = $requirements; stdout_limit_bytes = 4096; stderr_limit_bytes = 4096
    }
    Wait-Until { (Invoke-RestMethod "$httpAddress/v1/tasks/$($cancelTask.id)").status -eq "RUNNING" } "running cancellation task"
    Invoke-RestMethod -Method Post -Uri "$httpAddress/v1/tasks/$($cancelTask.id)/cancel" | Out-Null
    $cancelResult = Wait-Task $httpAddress $cancelTask.id "cancelled task"
    if ($cancelResult.status -ne "CANCELLED") {
        throw "Cancellation failed: $($cancelResult | ConvertTo-Json -Depth 10)"
    }

    $timeoutTask = Submit-Task $httpAddress @{
        type = "PROCESS"; version = "1"; executable = "cmd.exe"; arguments = $longArguments; timeout_ms = 200
        requirements = $requirements; stdout_limit_bytes = 4096; stderr_limit_bytes = 4096
    }
    $timeoutResult = Wait-Task $httpAddress $timeoutTask.id "timed-out task"
    if ($timeoutResult.status -ne "TIMED_OUT") {
        throw "Timeout handling failed: $($timeoutResult | ConvertTo-Json -Depth 10)"
    }

    $project = Join-Path $clean "project"
    New-Item -ItemType Directory -Path $project | Out-Null
    Set-Content -LiteralPath (Join-Path $project "nodren.json") -Value '{"entry_point":"project.py","runtime":"python"}' -Encoding utf8
    Set-Content -LiteralPath (Join-Path $project "project.py") -Value 'print("folder-ok")' -Encoding utf8
    $folderResult = (& (Join-Path $clean "nodren.exe") run $project 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $folderResult -notmatch "folder-ok") {
        throw "Folder/project execution failed: $folderResult"
    }

    $cliOutput = Join-Path $clean "cli-version.out"
    $cliError = Join-Path $clean "cli-version.err"
    $cliProcess = Start-Process -FilePath (Join-Path $clean "nodren.exe-CLI") -WorkingDirectory $clean -ArgumentList @("--version") -Wait -PassThru -WindowStyle Hidden -RedirectStandardOutput $cliOutput -RedirectStandardError $cliError
    $cliVersion = if (Test-Path -LiteralPath $cliOutput) { Get-Content -Raw -LiteralPath $cliOutput } else { "" }
    if ($cliProcess.ExitCode -ne 0 -or $cliVersion -notmatch "0\.2\.0") {
        throw "Clean release CLI failed: $cliVersion"
    }

    Write-Host "PASS: clean release directory, Controller, worker, native Core, process/command/script/artifact/cancel/timeout/folder tasks, sum=15, and separate CLI"
}
finally {
    if ($null -ne $workerProcess -and -not $workerProcess.HasExited) {
        Stop-Process -Id $workerProcess.Id -Force
    }
    if ($null -ne $controllerProcess -and -not $controllerProcess.HasExited) {
        Stop-Process -Id $controllerProcess.Id -Force
    }
    $env:NODREN_CORE_LIBRARY = $previousCoreLibrary
    Remove-Item -LiteralPath $clean -Recurse -Force -ErrorAction SilentlyContinue
}
