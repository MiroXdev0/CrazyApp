#include "../include/core_engine.hpp"

#include <algorithm>
#include <cstring>
#include <vector>

namespace nodren {

static std::int64_t sum_task(
    const std::int32_t* values,
    std::size_t count,
    void*) noexcept {
    return sum_i32(values, count);
}

std::int64_t xor_values(const std::int32_t* values, std::size_t count) noexcept {
    std::int64_t result = 0;
    for (std::size_t i = 0; i < count; ++i) {
        result ^= static_cast<std::int64_t>(values[i]);
    }
    return result;
}

struct DotContext {
    const std::int32_t* rhs = nullptr;
};

std::int64_t dot_values(const std::int32_t* values,
                        std::size_t count,
                        void* user_data) noexcept {
    const auto* context = static_cast<const DotContext*>(user_data);
    if (!values || !context || !context->rhs) return 0;

    std::int64_t result = 0;
    for (std::size_t i = 0; i < count; ++i) {
        result += static_cast<std::int64_t>(values[i]) * context->rhs[i];
    }
    return result;
}

enum class ParallelReduction { Sum, Xor, Dot };

std::int64_t parallel_reduce(ThreadPool& pool,
                             std::mutex& mutex,
                             const std::int32_t* lhs,
                             const std::int32_t* rhs,
                             std::size_t count,
                             ParallelReduction reduction) noexcept {
    if (!lhs || count == 0) return 0;

    std::lock_guard<std::mutex> lock(mutex);
    pool.wait_idle();
    const std::size_t worker_count = pool.worker_count();
    if (worker_count <= 1 || count < worker_count) {
        if (reduction == ParallelReduction::Sum) return sum_i32(lhs, count);
        if (reduction == ParallelReduction::Xor) return xor_values(lhs, count);
        DotContext context{rhs};
        return dot_values(lhs, count, &context);
    }

    const std::size_t chunks = std::min(worker_count, count);
    std::vector<std::int64_t> partials(chunks, 0);
    std::vector<DotContext> contexts(chunks);
    bool submitted = true;
    for (std::size_t index = 0; index < chunks; ++index) {
        const std::size_t begin = count * index / chunks;
        const std::size_t end = count * (index + 1) / chunks;
        Task task{};
        task.payload = lhs + begin;
        task.count = end - begin;
        task.result = &partials[index];
        if (reduction == ParallelReduction::Sum) {
            task.fn = sum_task;
        } else if (reduction == ParallelReduction::Xor) {
            task.fn = [](const std::int32_t* values, std::size_t size, void*) noexcept {
                return xor_values(values, size);
            };
        } else {
            contexts[index].rhs = rhs + begin;
            task.fn = dot_values;
            task.user_data = &contexts[index];
        }
        if (!pool.submit(task)) {
            submitted = false;
            break;
        }
    }
    pool.wait_idle();
    if (!submitted) {
        if (reduction == ParallelReduction::Sum) return sum_i32(lhs, count);
        if (reduction == ParallelReduction::Xor) return xor_values(lhs, count);
        DotContext context{rhs};
        return dot_values(lhs, count, &context);
    }

    std::int64_t result = 0;
    for (const auto partial : partials) {
        if (reduction == ParallelReduction::Xor) {
            result ^= partial;
        } else {
            result += partial;
        }
    }
    return result;
}

CoreEngine::CoreEngine(std::size_t worker_count, std::size_t queue_capacity)
    : pool_(
          worker_count == 0 ? std::thread::hardware_concurrency() : worker_count,
          queue_capacity) {
}

CoreEngine::~CoreEngine() {
    shutdown();
}

bool CoreEngine::initialize() noexcept {
    if (initialized_) return true;

    cpu_ = detect_cpu_features();

    const std::size_t arena_size = 16u * 1024u * 1024u;
    if (nodren_arena_init(&arena_, arena_size, 64) != 0) {
        return false;
    }

    initialized_ = true;
    return true;
}

void CoreEngine::shutdown() noexcept {
    if (!initialized_) return;

    pool_.wait_idle();
    nodren_arena_destroy(&arena_);
    initialized_ = false;
}

bool CoreEngine::submit(const Task& task) noexcept {
    if (!initialized_ || task.payload == nullptr || task.count == 0) {
        return false;
    }

    Task copy = task;
    if (copy.fn == nullptr) copy.fn = sum_task;
    return pool_.submit(copy);
}

void CoreEngine::wait_idle() noexcept {
    pool_.wait_idle();
}

TaskResult CoreEngine::execute_sync(const Task& task) noexcept {
    TaskResult result{};
    result.id = task.id;

    if (!initialized_) {
        result.state = TaskState::Failed;
        return result;
    }

    if (!task.payload || task.count == 0) {
        result.state = TaskState::Failed;
        return result;
    }

    result.state = TaskState::Running;
    result.value = task.fn
        ? task.fn(task.payload, task.count, task.user_data)
        : sum_task(task.payload, task.count, nullptr);
    result.state = TaskState::Completed;
    return result;
}

std::int64_t CoreEngine::parallel_sum_i32(const std::int32_t* values,
                                          std::size_t count) noexcept {
    return parallel_reduce(pool_, parallel_mutex_, values, nullptr, count,
                           ParallelReduction::Sum);
}

std::int64_t CoreEngine::parallel_xor_i32(const std::int32_t* values,
                                          std::size_t count) noexcept {
    return parallel_reduce(pool_, parallel_mutex_, values, nullptr, count,
                           ParallelReduction::Xor);
}

std::int64_t CoreEngine::parallel_dot_product_i32(const std::int32_t* lhs,
                                                  const std::int32_t* rhs,
                                                  std::size_t count) noexcept {
    return parallel_reduce(pool_, parallel_mutex_, lhs, rhs, count,
                           ParallelReduction::Dot);
}

} // namespace nodren
