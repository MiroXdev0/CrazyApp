$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$release = Join-Path $root "release"
$controller = Join-Path $root "Backend\Controller-Go"
$worker = Join-Path $root "Backend\Worker-Rust"
$uiProject = Join-Path $root "Frontend\Desktop\App\App.csproj"

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

$core = Get-ChildItem -LiteralPath (Join-Path $worker "target\release\build") -Filter "nodren_core.dll" -Recurse -File | Select-Object -First 1
if ($null -eq $core) {
    throw "Native Core release library was not produced"
}
Copy-Item -LiteralPath $core.FullName -Destination (Join-Path $release "nodren_core.dll")

$uiPublish = Join-Path $release ".ui-publish"
dotnet publish $uiProject --configuration Release --runtime win-x64 --self-contained true `
    -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true `
    -p:PublishTrimmed=false --output $uiPublish
if ($LASTEXITCODE -ne 0) { throw "Desktop UI release build failed" }

$uiBinary = Join-Path $uiPublish "nodren-ui.exe"
if (-not (Test-Path -LiteralPath $uiBinary)) {
    throw "Desktop UI release binary was not produced: $uiBinary"
}
Get-ChildItem -LiteralPath $uiPublish -File |
    Where-Object { $_.Extension -notin @('.pdb', '.xml') } |
    Copy-Item -Destination $release -Force
Remove-Item -LiteralPath $uiPublish -Recurse -Force

foreach ($artifact in @('nodren.exe', 'nodren-worker.exe', 'nodren-ui.exe', 'nodren_core.dll')) {
    if (-not (Test-Path -LiteralPath (Join-Path $release $artifact))) {
        throw "Required release artifact is missing: $artifact"
    }
}

Write-Host "Release artifacts:"
Get-ChildItem -LiteralPath $release | Select-Object Name, Length
