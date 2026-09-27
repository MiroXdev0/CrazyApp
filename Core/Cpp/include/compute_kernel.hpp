#pragma once

#include <cstddef>
#include <cstdint>

namespace nodren {

struct CpuFeatures {
    bool sse2 = false;
    bool avx = false;
    bool avx2 = false;
    std::size_t hardware_threads = 1;
};

CpuFeatures detect_cpu_features() noexcept;

std::int64_t scalar_sum_i32(const std::int32_t* values, std::size_t count) noexcept;
std::int64_t sum_i32(const std::int32_t* values, std::size_t count) noexcept;

} // namespace nodren

extern "C" {
std::int64_t nodren_asm_sum_i32(const std::int32_t* values, std::size_t count);
}
