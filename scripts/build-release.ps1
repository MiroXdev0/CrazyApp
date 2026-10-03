param(
    [string]$ReleaseDirectory = ""
)

$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$release = if ([string]::IsNullOrWhiteSpace($ReleaseDirectory)) {
    Join-Path $root "Nodren-1.0.0-windows-x64"
} else {
    [System.IO.Path]::GetFullPath($ReleaseDirectory)
}
$controller = Join-Path $root "Backend\Controller-Go"
$worker = Join-Path $root "Backend\Worker-Rust"
$cli = Join-Path $root "CLI\Rust"
$publish = Join-Path $root ".artifacts\NodrenApp-win-x64"
$controllerBinary = Join-Path $root ".artifacts\nodren-controller-win-x64.exe"

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $controllerBinary) | Out-Null
if (Test-Path -LiteralPath $publish) {
    Remove-Item -LiteralPath $publish -Recurse -Force
}
if (Test-Path -LiteralPath $release) {
    Remove-Item -LiteralPath $release -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $release | Out-Null

$vswhere = Join-Path ${env:ProgramFiles(x86)} "Microsoft Visual Studio\Installer\vswhere.exe"
if (-not (Test-Path -LiteralPath $vswhere)) {
    throw "Visual Studio Build Tools are required to build the Windows native Core"
}
$vcvars = & $vswhere -latest -products '*' -find 'VC\Auxiliary\Build\vcvars64.bat' | Select-Object -First 1
if ([string]::IsNullOrWhiteSpace($vcvars)) {
    throw "Visual Studio C++ Build Tools (vcvars64.bat) were not found"
}
function Invoke-VisualStudioCargoBuild([string]$Arguments, [string]$Description) {
    $cargoCommand = "call `"$vcvars`" >nul && set `"RUSTFLAGS=-C target-feature=+crt-static`" && cargo $Arguments"
    & $env:ComSpec /d /s /c $cargoCommand
    if ($LASTEXITCODE -ne 0) { throw "$Description failed" }
}

$clang = Get-Command clang.exe -ErrorAction SilentlyContinue
if ($null -eq $clang) {
    $clangPath = Join-Path $env:ProgramFiles "LLVM\bin"
    if (-not (Test-Path -LiteralPath (Join-Path $clangPath "clang.exe"))) {
        $clangPath = & $vswhere -latest -products '*' -find 'VC\Tools\Llvm\bin\clang.exe' | Select-Object -First 1
        if ([string]::IsNullOrWhiteSpace($clangPath)) {
            throw "LLVM clang is required to assemble the Windows Core kernel"
        }
        $clangPath = Split-Path -Parent $clangPath
    }
    $env:PATH = "$clangPath;$env:PATH"
}

Push-Location $controller
try {
    go build -trimpath -ldflags "-s -w" -o $controllerBinary .
    if ($LASTEXITCODE -ne 0) { throw "Controller release build failed" }
}
finally {
    Pop-Location
}

Invoke-VisualStudioCargoBuild "build --release --bin nodren-worker --manifest-path `"$worker\Cargo.toml`"" "Worker release build"

$workerBinary = Join-Path $worker "target\release\nodren-worker.exe"
if (-not (Test-Path -LiteralPath $workerBinary)) {
    throw "Worker release binary was not produced: $workerBinary"
}
Copy-Item -LiteralPath $workerBinary -Destination (Join-Path $release "nodren-worker.exe")

$core = Get-ChildItem -LiteralPath (Join-Path $worker "target\release\build") -Filter "nodren-core.dll" -Recurse -File |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
if ($null -eq $core) {
    throw "Native Core release library was not produced"
}
Copy-Item -LiteralPath $core.FullName -Destination (Join-Path $release "nodren-core.dll")

Push-Location $cli
try {
    Invoke-VisualStudioCargoBuild "build --release --bin nodren --manifest-path `"$cli\Cargo.toml`"" "CLI release build"
}
finally {
    Pop-Location
}

$cliBinary = Join-Path $cli "target\release\nodren.exe"
if (-not (Test-Path -LiteralPath $cliBinary)) {
    throw "CLI release binary was not produced: $cliBinary"
}
Copy-Item -LiteralPath $cliBinary -Destination (Join-Path $release "nodren.exe")

dotnet publish (Join-Path $root "Frontend\Desktop\App\App.csproj") `
    --configuration Release `
    --runtime win-x64 `
    --self-contained true `
    -p:PublishSingleFile=true `
    -p:IncludeNativeLibrariesForSelfExtract=true `
    -p:PublishTrimmed=false `
    "-p:NodrenControllerPath=$controllerBinary" `
    --output $publish
if ($LASTEXITCODE -ne 0) { throw "NodrenApp application publish failed" }

$applicationBinary = Join-Path $publish "NodrenApp.exe"
if (-not (Test-Path -LiteralPath $applicationBinary)) {
    throw "NodrenApp executable was not produced: $applicationBinary"
}
Copy-Item -LiteralPath $applicationBinary -Destination (Join-Path $release "NodrenApp.exe")

$expected = @('NodrenApp.exe', 'nodren.exe', 'nodren-worker.exe', 'nodren-core.dll') | Sort-Object
$actual = @(Get-ChildItem -LiteralPath $release -File | Select-Object -ExpandProperty Name | Sort-Object)
if (@(Compare-Object -ReferenceObject $expected -DifferenceObject $actual).Count -ne 0 -or
    @(Get-ChildItem -LiteralPath $release -Force).Count -ne 4) {
    throw "Release directory does not contain exactly the four required artifacts"
}

Remove-Item -LiteralPath $publish -Recurse -Force
Remove-Item -LiteralPath $controllerBinary -Force

Write-Host "Release directory: $release"
Write-Host "Release artifacts:"
Get-ChildItem -LiteralPath $release | Select-Object Name, Length
