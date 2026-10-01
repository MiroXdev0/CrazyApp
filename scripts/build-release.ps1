$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$release = Join-Path $root "release"
$controller = Join-Path $root "Backend\Controller-Go"
$worker = Join-Path $root "Backend\Worker-Rust"
$cli = Join-Path $root "CLI\Rust"

if (Test-Path -LiteralPath $release) {
    Remove-Item -LiteralPath $release -Recurse -Force
}
New-Item -ItemType Directory -Path $release | Out-Null

Push-Location $controller
try {
    go build -trimpath -ldflags "-s -w" -o (Join-Path $release "nodren.exe") .
    if ($LASTEXITCODE -ne 0) { throw "Controller release build failed" }
}
finally {
    Pop-Location
}

cargo build --release --bin nodren-worker --manifest-path (Join-Path $worker "Cargo.toml")
if ($LASTEXITCODE -ne 0) { throw "Worker release build failed" }

$workerBinary = Join-Path $worker "target\release\nodren-worker.exe"
if (-not (Test-Path -LiteralPath $workerBinary)) {
    throw "Worker release binary was not produced: $workerBinary"
}
Copy-Item -LiteralPath $workerBinary -Destination (Join-Path $release "nodren-worker.exe")

$core = Get-ChildItem -LiteralPath (Join-Path $worker "target\release\build") -Filter "nodren-core.dll" -Recurse -File | Select-Object -First 1
if ($null -eq $core) {
    throw "Native Core release library was not produced"
}
Copy-Item -LiteralPath $core.FullName -Destination (Join-Path $release "nodren-core.dll")

Push-Location $cli
try {
    cargo build --release --bin nodren --manifest-path (Join-Path $cli "Cargo.toml")
    if ($LASTEXITCODE -ne 0) { throw "CLI release build failed" }
}
finally {
    Pop-Location
}

$cliBinary = Join-Path $cli "target\release\nodren.exe"
if (-not (Test-Path -LiteralPath $cliBinary)) {
    throw "CLI release binary was not produced: $cliBinary"
}
Copy-Item -LiteralPath $cliBinary -Destination (Join-Path $release "nodren.exe-CLI")

foreach ($artifact in @('nodren.exe', 'nodren.exe-CLI', 'nodren-worker.exe', 'nodren-core.dll')) {
    if (-not (Test-Path -LiteralPath (Join-Path $release $artifact))) {
        throw "Required release artifact is missing: $artifact"
    }
}

$actual = @(Get-ChildItem -LiteralPath $release -File | Select-Object -ExpandProperty Name | Sort-Object)
$expected = @('nodren-core.dll', 'nodren-worker.exe', 'nodren.exe', 'nodren.exe-CLI') | Sort-Object
if (@(Compare-Object -ReferenceObject $expected -DifferenceObject $actual).Count -ne 0) {
    throw "Release directory contains files other than the four required Windows artifacts"
}

Write-Host "Release artifacts:"
Get-ChildItem -LiteralPath $release | Select-Object Name, Length
