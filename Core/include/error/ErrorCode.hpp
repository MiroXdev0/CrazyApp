#pragma once

#include <cstdint>

enum class ErrorCode : int {
    None = 0,

    NetworkFailure,
    NodeDisconnected,
    ProtocolError,

    InvalidJob,
    InvalidTask,
    TaskExecutionFailed,

    MemoryAllocationFailed,
    ResourceUnavailable,

    CoreFailure,
    Unknown
};

enum class Severity : int {
    Trace = 0,
    Info,
    Warning,
    Error,
    Critical
};
