#pragma once

#include "compute_kernel.hpp"
#include "core_types.hpp"
#include "thread_pool.hpp"

#include "../../C/include/nodren_memory.h"

#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>

namespace nodren {

class CoreEngine final {
public:
    explicit CoreEngine(std::size_t worker_count = 0,
                        std::size_t queue_capacity = 4096);
    ~CoreEngine();

    CoreEngine(const CoreEngine&) = delete;
    CoreEngine& operator=(const CoreEngine&) = delete;

    bool initialize() noexcept;
    void shutdown() noexcept;

    bool submit(const Task& task) noexcept;
    void wait_idle() noexcept;

    TaskResult execute_sync(const Task& task) noexcept;

    // Large built-in reductions use the Core pool. The serialized parallel
    // section prevents several full-width reductions from oversubscribing the
    // same machine while still allowing the pool to use all configured CPUs.
    std::int64_t parallel_sum_i32(const std::int32_t* values,
                                  std::size_t count) noexcept;
    std::int64_t parallel_xor_i32(const std::int32_t* values,
                                  std::size_t count) noexcept;
    std::int64_t parallel_dot_product_i32(const std::int32_t* lhs,
                                          const std::int32_t* rhs,
                                          std::size_t count) noexcept;

    const CpuFeatures& cpu() const noexcept { return cpu_; }
    std::size_t worker_count() const noexcept { return pool_.worker_count(); }

private:
    CpuFeatures cpu_{};
    NodrenArena arena_{};
    ThreadPool pool_;
    std::mutex parallel_mutex_;
    bool initialized_ = false;
};

} // namespace nodren
