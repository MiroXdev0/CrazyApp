#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD="$ROOT/build"
mkdir -p "$BUILD"

# Keep the Linux release portable across x86-64 machines. Runtime AVX2
# dispatch remains in compute_kernel.cpp; this build does not assume the
# build host has a particular microarchitecture.
CFLAGS=(-std=c11 -O3 -DNDEBUG -fPIC -I"$ROOT/C/include")
CXXFLAGS=(-std=c++20 -O3 -DNDEBUG -fPIC -I"$ROOT/C/include" -I"$ROOT/Cpp/include")

gcc "${CFLAGS[@]}" -c "$ROOT/C/src/nodren_memory.c" -o "$BUILD/nodren_memory.o"
g++ "${CXXFLAGS[@]}" -c "$ROOT/Cpp/src/compute_kernel.cpp" -o "$BUILD/compute_kernel.o"
g++ "${CXXFLAGS[@]}" -c "$ROOT/Cpp/src/thread_pool.cpp" -o "$BUILD/thread_pool.o"
g++ "${CXXFLAGS[@]}" -c "$ROOT/Cpp/src/core_engine.cpp" -o "$BUILD/core_engine.o"
g++ "${CXXFLAGS[@]}" -c "$ROOT/Cpp/src/nodren_c_api.cpp" -o "$BUILD/nodren_c_api.o"
g++ "${CXXFLAGS[@]}" -c "$ROOT/Cpp/src/workload_dispatch.cpp" -o "$BUILD/workload_dispatch.o"
g++ -c -O3 -fPIC "$ROOT/Assembly/X64/Linux/sum_avx2.S" -o "$BUILD/sum_avx2.o"

OBJECTS=(
    "$BUILD/nodren_memory.o"
    "$BUILD/compute_kernel.o"
    "$BUILD/thread_pool.o"
    "$BUILD/core_engine.o"
    "$BUILD/nodren_c_api.o"
    "$BUILD/workload_dispatch.o"
    "$BUILD/sum_avx2.o"
)

g++ -shared "${OBJECTS[@]}" -pthread -o "$BUILD/libnodren_core.so"

g++ "${CXXFLAGS[@]}" "$ROOT/Cpp/src/main.cpp" "${OBJECTS[@]}" -pthread -o "$BUILD/nodren_core"
g++ "${CXXFLAGS[@]}" "$ROOT/Cpp/tests/core_selftest.cpp" "${OBJECTS[@]}" -pthread -o "$BUILD/nodren_core_selftest"

"$BUILD/nodren_core_selftest"
"$BUILD/nodren_core"

for symbol in nodren_core_create nodren_core_initialize nodren_core_destroy nodren_core_execute_workload; do
    if ! nm -D --defined-only "$BUILD/libnodren_core.so" | awk '{print $3}' | grep -Fxq "$symbol"; then
        echo "Required native symbol is missing: $symbol" >&2
        exit 1
    fi
done

echo "Built Linux native Core: $BUILD/libnodren_core.so"
