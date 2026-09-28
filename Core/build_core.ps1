$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$build = Join-Path $root "build"
$out = Join-Path $build "nodren_core.exe"

New-Item -ItemType Directory -Force -Path $build | Out-Null

# This script targets MinGW/Clang-style GCC toolchains on Windows.
# The Windows ABI assembly kernel uses RCX/RDX.
gcc -std=c11 -O3 -DNDEBUG `
    -I"$root\C\include" `
    -c "$root\C\src\nodren_memory.c" `
    -o "$build\nodren_memory.o"

g++ -std=c++20 -O3 -DNDEBUG `
    -I"$root\C\include" -I"$root\Cpp\include" `
    -c "$root\Cpp\src\compute_kernel.cpp" `
    -o "$build\compute_kernel.o"

g++ -std=c++20 -O3 -DNDEBUG `
    -I"$root\C\include" -I"$root\Cpp\include" `
    -c "$root\Cpp\src\thread_pool.cpp" `
    -o "$build\thread_pool.o"

g++ -std=c++20 -O3 -DNDEBUG `
    -I"$root\C\include" -I"$root\Cpp\include" `
    -c "$root\Cpp\src\core_engine.cpp" `
    -o "$build\core_engine.o"

g++ -std=c++20 -O3 -DNDEBUG `
    -I"$root\C\include" -I"$root\Cpp\include" `
    -c "$root\Cpp\src\nodren_c_api.cpp" `
    -o "$build\nodren_c_api.o"

g++ -std=c++20 -O3 -DNDEBUG `
    -I"$root\C\include" -I"$root\Cpp\include" `
    -c "$root\Cpp\src\workload_dispatch.cpp" `
    -o "$build\workload_dispatch.o"

g++ -c -O3 "$root\Assembly\X64\Windows\sum_avx2.S" `
    -o "$build\sum_avx2.o"

g++ -std=c++20 -O3 -DNDEBUG `
    -I"$root\C\include" -I"$root\Cpp\include" `
    "$root\Cpp\src\main.cpp" `
    "$build\nodren_memory.o" `
    "$build\compute_kernel.o" `
    "$build\thread_pool.o" `
    "$build\core_engine.o" `
    "$build\nodren_c_api.o" `
    "$build\workload_dispatch.o" `
    "$build\sum_avx2.o" `
    -o "$out"

Write-Host "Built: $out"
& $out


g++ -std=c++20 -O3 -DNDEBUG `
    -I"$root\C\include" -I"$root\Cpp\include" `
    "$root\Cpp\tests\core_selftest.cpp" `
    "$build\nodren_memory.o" `
    "$build\compute_kernel.o" `
    "$build\thread_pool.o" `
    "$build\core_engine.o" `
    "$build\nodren_c_api.o" `
    "$build\workload_dispatch.o" `
    "$build\sum_avx2.o" `
    -o "$build\nodren_core_selftest.exe"

& "$build\nodren_core_selftest.exe"
