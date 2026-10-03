param(
    [Parameter(Mandatory = $true)]
    [string]$Controller,
    [Parameter(Mandatory = $true)]
    [string]$HttpController,
    [string]$ReleaseDirectory = (Join-Path $PSScriptRoot "..\..\release"),
    [string]$WorkerId = "REMOTE-WORKER"
)

$ErrorActionPreference = "Stop"
$workerPath = Join-Path $ReleaseDirectory "nodren-worker.exe"
$cliPath = Join-Path $ReleaseDirectory "nodren.exe"
if (-not (Test-Path -LiteralPath $workerPath) -or -not (Test-Path -LiteralPath $cliPath)) {
    throw "Build the release directory before running this test"
}
if ([string]::IsNullOrWhiteSpace($env:NODREN_API_TOKEN) -or [string]::IsNullOrWhiteSpace($env:NODREN_WORKER_TOKEN)) {
    throw "Set NODREN_API_TOKEN and NODREN_WORKER_TOKEN for the secure remote integration test"
}
$httpUri = [Uri]$HttpController
if ($httpUri.Scheme -ne "https") {
    throw "Secure remote API access requires an HTTPS URL"
}

$env:NODREN_CONTROLLER_URL = $HttpController.TrimEnd("/")
$env:NODREN_AUTH_MODE = "secure"
$worker = $null
$apiHeaders = @{ Authorization = "Bearer $env:NODREN_API_TOKEN" }

function Wait-WorkerState([string]$ExpectedState, [int]$Seconds = 30) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $nodes = @(Invoke-RestMethod "$env:NODREN_CONTROLLER_URL/v1/nodes" -Headers $apiHeaders)
            $node = $nodes | Where-Object { $_.info.id -eq $WorkerId }
            if ($null -ne $node -and $node.state -eq $ExpectedState) {
                return
            }
        }
        catch {
        }
        Start-Sleep -Milliseconds 250
    }
    throw "Timed out waiting for $WorkerId to become $ExpectedState"
}

function Start-RemoteWorker {
    return Start-Process -FilePath $workerPath -WorkingDirectory $ReleaseDirectory -PassThru -WindowStyle Hidden -ArgumentList @("--controller", $Controller, "--id", $WorkerId)
}

try {
    $worker = Start-RemoteWorker
    Wait-WorkerState "READY"
    $sum = (& $cliPath run sum 1 2 3 4 5 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $sum -notmatch "Result\s+15") {
        throw "Remote sum failed: $sum"
    }

    Stop-Process -Id $worker.Id -Force
    $worker = $null
    Wait-WorkerState "LOST"

    $worker = Start-RemoteWorker
    Wait-WorkerState "READY"
    $xor = (& $cliPath run xor 1 2 3 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $xor -notmatch "Result\s+0") {
        throw "Remote reconnect workload failed: $xor"
    }
    Write-Host "PASS: remote worker registration, sum=15, disconnect, reconnect, and xor=0"
}
finally {
    if ($null -ne $worker -and -not $worker.HasExited) {
        Stop-Process -Id $worker.Id -Force
    }
}
