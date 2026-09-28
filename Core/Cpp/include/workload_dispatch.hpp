#pragma once

#include "core_types.hpp"

#include <cstddef>
#include <cstdint>
#include <string_view>

namespace nodren {

class CoreEngine;

enum class WorkloadError : int {
    None = 0,
    InvalidArgument = 1,
    MalformedPayload = 2,
    UnsupportedWorkload = 3,
    ExecutionFailed = 4,
    Internal = 5,
};

struct WorkloadResult {
    TaskResult task{};
    WorkloadError error = WorkloadError::None;
};

WorkloadResult dispatch_workload(
    CoreEngine& engine,
    TaskId task_id,
    std::string_view command,
    const std::uint8_t* payload,
    std::size_t payload_size);

} // namespace nodren
