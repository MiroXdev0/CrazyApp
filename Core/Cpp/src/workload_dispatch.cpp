#include "../include/workload_dispatch.hpp"

#include "../include/compute_kernel.hpp"
#include "../include/core_engine.hpp"

#include <cstring>
#include <limits>
#include <vector>

namespace nodren {
namespace {

std::int64_t xor_bytes(
    const std::int32_t* values,
    std::size_t count,
    void*) noexcept {
    std::int64_t result = 0;
    for (std::size_t i = 0; i < count; ++i) {
        result ^= static_cast<std::int64_t>(values[i]);
    }
    return result;
}

std::int64_t dot_product(
    const std::int32_t* values,
    std::size_t count,
    void* user_data) noexcept {
    const auto* rhs = static_cast<const std::int32_t*>(user_data);
    if (!rhs) return 0;

    std::int64_t result = 0;
    for (std::size_t i = 0; i < count; ++i) {
        result += static_cast<std::int64_t>(values[i]) * rhs[i];
    }
    return result;
}

bool read_u32(
    const std::uint8_t* payload,
    std::size_t payload_size,
    std::size_t offset,
    std::uint32_t* out) noexcept {
    if (!payload || !out || offset > payload_size || payload_size - offset < 4) {
        return false;
    }
    *out = static_cast<std::uint32_t>(payload[offset])
        | (static_cast<std::uint32_t>(payload[offset + 1]) << 8u)
        | (static_cast<std::uint32_t>(payload[offset + 2]) << 16u)
        | (static_cast<std::uint32_t>(payload[offset + 3]) << 24u);
    return true;
}

std::int32_t read_i32(const std::uint8_t* payload, std::size_t offset) noexcept {
    std::uint32_t bits = static_cast<std::uint32_t>(payload[offset])
        | (static_cast<std::uint32_t>(payload[offset + 1]) << 8u)
        | (static_cast<std::uint32_t>(payload[offset + 2]) << 16u)
        | (static_cast<std::uint32_t>(payload[offset + 3]) << 24u);
    std::int32_t value = 0;
    std::memcpy(&value, &bits, sizeof(value));
    return value;
}

WorkloadResult failed(TaskId task_id, WorkloadError error) noexcept {
    WorkloadResult result{};
    result.task.id = task_id;
    result.task.state = TaskState::Failed;
    result.error = error;
    return result;
}

} // namespace

WorkloadResult dispatch_workload(
    CoreEngine& engine,
    TaskId task_id,
    std::string_view command,
    const std::uint8_t* payload,
    std::size_t payload_size) {
    if (!payload || payload_size == 0) {
        return failed(task_id, WorkloadError::MalformedPayload);
    }

    if (command == "sum" || command == "xor") {
        std::vector<std::int32_t> values(payload_size);
        for (std::size_t i = 0; i < payload_size; ++i) {
            values[i] = static_cast<std::int32_t>(payload[i]);
        }

        Task task{};
        task.id = task_id;
        task.payload = values.data();
        task.count = values.size();
        task.fn = command == "sum" ? nullptr : xor_bytes;
        const TaskResult result = engine.execute_sync(task);
        if (result.state != TaskState::Completed) {
            return failed(task_id, WorkloadError::ExecutionFailed);
        }
        return WorkloadResult{result, WorkloadError::None};
    }

    if (command != "dot_product") {
        return failed(task_id, WorkloadError::UnsupportedWorkload);
    }

    std::uint32_t count = 0;
    if (!read_u32(payload, payload_size, 0, &count) || count == 0) {
        return failed(task_id, WorkloadError::MalformedPayload);
    }

    const std::size_t element_bytes = sizeof(std::int32_t);
    if (count > (std::numeric_limits<std::size_t>::max() - 4) / (element_bytes * 2)) {
        return failed(task_id, WorkloadError::MalformedPayload);
    }
    const std::size_t expected_size = 4 + static_cast<std::size_t>(count) * element_bytes * 2;
    if (expected_size != payload_size) {
        return failed(task_id, WorkloadError::MalformedPayload);
    }

    std::vector<std::int32_t> lhs(count);
    std::vector<std::int32_t> rhs(count);
    const std::size_t rhs_offset = 4 + static_cast<std::size_t>(count) * element_bytes;
    for (std::size_t i = 0; i < count; ++i) {
        lhs[i] = read_i32(payload, 4 + i * element_bytes);
        rhs[i] = read_i32(payload, rhs_offset + i * element_bytes);
    }

    Task task{};
    task.id = task_id;
    task.payload = lhs.data();
    task.count = lhs.size();
    task.fn = dot_product;
    task.user_data = rhs.data();
    const TaskResult result = engine.execute_sync(task);
    if (result.state != TaskState::Completed) {
        return failed(task_id, WorkloadError::ExecutionFailed);
    }
    return WorkloadResult{result, WorkloadError::None};
}

} // namespace nodren
