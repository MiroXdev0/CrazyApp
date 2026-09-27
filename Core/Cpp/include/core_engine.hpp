#pragma once

#include "compute_kernel.hpp"
#include "core_types.hpp"
#include "thread_pool.hpp"

#include "../../C/include/nodren_memory.h"

#include <cstddef>
#include <cstdint>
#include <memory>

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

    const CpuFeatures& cpu() const noexcept { return cpu_; }
    std::size_t worker_count() const noexcept { return pool_.worker_count(); }

private:
    CpuFeatures cpu_{};
    NodrenArena arena_{};
    ThreadPool pool_;
    bool initialized_ = false;
};

} // namespace nodren
