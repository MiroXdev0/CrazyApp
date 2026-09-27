#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$ROOT/build/nodren_core"

mkdir -p "$ROOT/build"

gcc -std=c11 -O3 -DNDEBUG -I"$ROOT/C/include" \
    -c "$ROOT/C/src/nodren_memory.c" \
    -o "$ROOT/build/nodren_memory.o"

g++ -std=c++20 -O3 -DNDEBUG -march=native \
    -I"$ROOT/C/include" -I"$ROOT/Cpp/include" \
    -c "$ROOT/Cpp/src/compute_kernel.cpp" \
    -o "$ROOT/build/compute_kernel.o"

g++ -std=c++20 -O3 -DNDEBUG -march=native \
    -I"$ROOT/C/include" -I"$ROOT/Cpp/include" \
    -c "$ROOT/Cpp/src/thread_pool.cpp" \
    -o "$ROOT/build/thread_pool.o"

g++ -std=c++20 -O3 -DNDEBUG -march=native \
    -I"$ROOT/C/include" -I"$ROOT/Cpp/include" \
    -c "$ROOT/Cpp/src/core_engine.cpp" \
    -o "$ROOT/build/core_engine.o"

g++ -std=c++20 -O3 -DNDEBUG -march=native \
    -I"$ROOT/C/include" -I"$ROOT/Cpp/include" \
    -c "$ROOT/Cpp/src/nodren_c_api.cpp" \
    -o "$ROOT/build/nodren_c_api.o"

g++ -c -O3 "$ROOT/Assembly/X64/Linux/sum_avx2.S" \
    -o "$ROOT/build/sum_avx2.o"

g++ -std=c++20 -O3 -DNDEBUG -march=native \
    -I"$ROOT/C/include" -I"$ROOT/Cpp/include" \
    "$ROOT/Cpp/src/main.cpp" \
    "$ROOT/build/nodren_memory.o" \
    "$ROOT/build/compute_kernel.o" \
    "$ROOT/build/thread_pool.o" \
    "$ROOT/build/core_engine.o" \
    "$ROOT/build/nodren_c_api.o" \
    "$ROOT/build/sum_avx2.o" \
    -pthread -o "$OUT"


g++ -std=c++20 -O3 -DNDEBUG -march=native \
    -I"$ROOT/C/include" -I"$ROOT/Cpp/include" \
    "$ROOT/Cpp/tests/core_selftest.cpp" \
    "$ROOT/build/nodren_memory.o" \
    "$ROOT/build/compute_kernel.o" \
    "$ROOT/build/thread_pool.o" \
    "$ROOT/build/core_engine.o" \
    "$ROOT/build/nodren_c_api.o" \
    "$ROOT/build/sum_avx2.o" \
    -pthread -o "$ROOT/build/nodren_core_selftest"

"$ROOT/build/nodren_core_selftest"

echo "Built: $OUT"
"$OUT"


g++ -std=c++20 -O3 -DNDEBUG -march=native \
    -I"$ROOT/C/include" -I"$ROOT/Cpp/include" \
    "$ROOT/Cpp/tests/core_selftest.cpp" \
    "$ROOT/build/nodren_memory.o" \
    "$ROOT/build/compute_kernel.o" \
    "$ROOT/build/thread_pool.o" \
    "$ROOT/build/core_engine.o" \
    "$ROOT/build/nodren_c_api.o" \
    "$ROOT/build/sum_avx2.o" \
    -pthread -o "$ROOT/build/nodren_core_selftest"

"$ROOT/build/nodren_core_selftest"
