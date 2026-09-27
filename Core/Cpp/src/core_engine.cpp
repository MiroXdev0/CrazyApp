#include "../include/core_engine.hpp"

#include <algorithm>
#include <cstring>

namespace nodren {

static std::int64_t sum_task(
    const std::int32_t* values,
    std::size_t count,
    void*) noexcept {
    return sum_i32(values, count);
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

} // namespace nodren
