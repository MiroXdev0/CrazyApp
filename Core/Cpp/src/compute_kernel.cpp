#include "../include/compute_kernel.hpp"

#include <thread>

namespace nodren {

CpuFeatures detect_cpu_features() noexcept {
    CpuFeatures features{};

#if defined(__x86_64__) || defined(_M_X64)
    #if defined(__GNUC__) || defined(__clang__)
        __builtin_cpu_init();
        features.sse2 = __builtin_cpu_supports("sse2");
        features.avx = __builtin_cpu_supports("avx");
        features.avx2 = __builtin_cpu_supports("avx2");
    #endif
#endif

    features.hardware_threads = std::thread::hardware_concurrency();
    if (features.hardware_threads == 0) features.hardware_threads = 1;

    return features;
}

std::int64_t scalar_sum_i32(
    const std::int32_t* values,
    std::size_t count) noexcept {
    if (!values) return 0;

    std::int64_t total = 0;
    for (std::size_t i = 0; i < count; ++i) {
        total += static_cast<std::int64_t>(values[i]);
    }
    return total;
}

std::int64_t sum_i32(
    const std::int32_t* values,
    std::size_t count) noexcept {
    if (!values || count == 0) return 0;

#if defined(__x86_64__) || defined(_M_X64)
    static const CpuFeatures features = detect_cpu_features();
    if (features.avx2) {
        return nodren_asm_sum_i32(values, count);
    }
#endif

    return scalar_sum_i32(values, count);
}

} // namespace nodren
