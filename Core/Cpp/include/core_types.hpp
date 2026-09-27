#pragma once

#include <cstddef>
#include <cstdint>

namespace nodren {

using TaskId = std::uint64_t;

enum class TaskState : std::uint8_t {
    Queued,
    Running,
    Completed,
    Failed,
    Cancelled
};

using TaskFn = std::int64_t (*)(const std::int32_t*, std::size_t, void*) noexcept;

struct Task {
    TaskId id = 0;
    const std::int32_t* payload = nullptr;
    std::size_t count = 0;
    TaskFn fn = nullptr;
    void* user_data = nullptr;
    std::int64_t* result = nullptr;
};

struct TaskResult {
    TaskId id = 0;
    std::int64_t value = 0;
    TaskState state = TaskState::Failed;
};

} // namespace nodren
