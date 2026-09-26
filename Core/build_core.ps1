$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$outputDir = Join-Path $root "bin"
$binaryPath = Join-Path $outputDir "crazyapp_core.exe"

New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

$compileArgs = @(
    (Join-Path $root "C\source\memory.c"),
    (Join-Path $root "Cpp\source\resource_manager.cpp"),
    (Join-Path $root "Cpp\source\execution_engine.cpp"),
    (Join-Path $root "Cpp\source\task_system.cpp"),
    (Join-Path $root "Cpp\source\core.cpp"),
    (Join-Path $root "Assembly\X64\sum_kernel.S"),
    "-I$root\C\header",
    "-I$root\Cpp\header",
    "-std=c++17",
    "-O3",
    "-o",
    $binaryPath
)

Write-Host "[core] compiling to $binaryPath"
g++ @compileArgs

Write-Host "[core] launch: $binaryPath"
& $binaryPath
