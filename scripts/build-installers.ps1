param(
    [string]$ReleaseDirectory = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$version = "1.0.1"
$stage = Join-Path $root ".artifacts\Nodren-installer-stage"
$toolDirectory = Join-Path $root ".artifacts\tools\InnoSetup-6.7.3"
$output = if ([string]::IsNullOrWhiteSpace($ReleaseDirectory)) {
    Join-Path $root "Nodren-$version-windows-x64"
} else {
    [System.IO.Path]::GetFullPath($ReleaseDirectory)
}
$expectedFiles = @("NodrenApp.exe", "nodren.exe", "nodren-worker.exe", "nodren-core.dll")

if ($output -eq $root -or -not $output.StartsWith("$root\", [StringComparison]::OrdinalIgnoreCase)) {
    throw "Release output must be a child directory of the repository: $output"
}
if ($stage -eq $root -or -not $stage.StartsWith("$root\", [StringComparison]::OrdinalIgnoreCase)) {
    throw "Invalid installer staging directory: $stage"
}

Write-Host "[1/4] Building the Windows Nodren release payload"
& (Join-Path $PSScriptRoot "build-release.ps1") -ReleaseDirectory $stage
if ($LASTEXITCODE -ne 0) {
    throw "Windows release payload build failed"
}

$staged = @(Get-ChildItem -LiteralPath $stage -File | Select-Object -ExpandProperty Name | Sort-Object)
if (@(Compare-Object ($expectedFiles | Sort-Object) $staged -CaseSensitive).Count -ne 0) {
    throw "Staging release does not contain exactly the four expected Nodren files: $($staged -join ', ')"
}
foreach ($name in $expectedFiles) {
    $file = Join-Path $stage $name
    if ((Get-Item -LiteralPath $file).Length -le 0) {
        throw "Required release file is empty: $file"
    }
}

Write-Host "[2/4] Locating pinned Inno Setup 6.7.3 compiler"
$localCompiler = Join-Path $toolDirectory "ISCC.exe"
if (-not (Test-Path -LiteralPath $localCompiler)) {
    $installer = Join-Path $env:TEMP "innosetup-6.7.3-setup.exe"
    $installerUrl = "https://github.com/jrsoftware/issrc/releases/download/is-6_7_3/innosetup-6.7.3.exe"
    $expectedHash = "9C73C3BAE7ED48D44112A0F48E66742C00090BDB5BEF71D9D3C056C66E97B732"
    Invoke-WebRequest -Uri $installerUrl -OutFile $installer
    $actualHash = (Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash
    if ($actualHash -ne $expectedHash) {
        Remove-Item -LiteralPath $installer -Force -ErrorAction SilentlyContinue
        throw "Inno Setup compiler download failed SHA-256 verification."
    }
    New-Item -ItemType Directory -Path $toolDirectory -Force | Out-Null
    $install = Start-Process -FilePath $installer -ArgumentList @(
        "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
        "/CURRENTUSER", "/DIR=$toolDirectory"
    ) -Wait -PassThru
    Remove-Item -LiteralPath $installer -Force
    if ($install.ExitCode -ne 0 -or -not (Test-Path -LiteralPath $localCompiler)) {
        throw "Could not install the pinned Inno Setup compiler into the ignored .artifacts tool directory."
    }
}
$compilerPath = $localCompiler

Write-Host "[3/4] Building Nodren 1.0.1 installers"
New-Item -ItemType Directory -Path $output -Force | Out-Null
$previousVariables = @{}
foreach ($name in @("NODREN_INSTALLER_RELEASE_DIR", "NODREN_INSTALLER_OUTPUT_DIR", "NODREN_INSTALLER_VERSION")) {
    $previousVariables[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
$env:NODREN_INSTALLER_RELEASE_DIR = $stage
$env:NODREN_INSTALLER_OUTPUT_DIR = $output
$env:NODREN_INSTALLER_VERSION = $version
try {
    foreach ($script in @("Nodren.iss", "NodrenWorker.iss")) {
        & $compilerPath (Join-Path $PSScriptRoot "installers\$script")
        if ($LASTEXITCODE -ne 0) {
            throw "Inno Setup failed while compiling $script"
        }
    }
}
finally {
    foreach ($name in $previousVariables.Keys) {
        [Environment]::SetEnvironmentVariable($name, $previousVariables[$name], "Process")
    }
}

Write-Host "[4/4] Assembling and verifying the final release directory"
foreach ($name in $expectedFiles) {
    $sourceFile = Join-Path $stage $name
    $releaseFile = Join-Path $output $name
    Copy-Item -LiteralPath $sourceFile -Destination $releaseFile -Force
    if ((Get-FileHash -LiteralPath $sourceFile -Algorithm SHA256).Hash -ne
        (Get-FileHash -LiteralPath $releaseFile -Algorithm SHA256).Hash) {
        throw "Release payload integrity verification failed for $name"
    }
}
$expectedFinal = @($expectedFiles) + @("Install-Nodren.exe", "Install-Nodren-Worker.exe")
$actualFinal = @(Get-ChildItem -LiteralPath $output -File | Select-Object -ExpandProperty Name | Sort-Object)
if (@(Compare-Object ($expectedFinal | Sort-Object) $actualFinal -CaseSensitive).Count -ne 0) {
    throw "Installer release directory is incomplete or contains unexpected files: $($actualFinal -join ', ')"
}
foreach ($name in $expectedFinal) {
    if ((Get-Item -LiteralPath (Join-Path $output $name)).Length -le 0) {
        throw "Release artifact is empty: $name"
    }
}

Write-Host "Nodren $version Windows x64 release: $output"
Get-ChildItem -LiteralPath $output -File | Select-Object Name, Length
