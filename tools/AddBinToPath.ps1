$CliBin = Join-Path $PSScriptRoot "..\CLI_bin"
$CliBin = (Resolve-Path $CliBin).Path

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")

if ($userPath -notlike "*$CliBin*") {
    [Environment]::SetEnvironmentVariable(
        "Path",
        "$userPath;$CliBin",
        "User"
    )
}

$env:Path = "$env:Path;$CliBin"

Write-Host "[OK] CLI_bin added to PATH"
Write-Host $CliBin