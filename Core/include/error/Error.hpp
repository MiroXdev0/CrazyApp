#pragma once

#include <cstdint>
#include <ctime>
#include <string>
#include <utility>

#include "ErrorCode.hpp"

class Error {
public:
    ErrorCode code{ErrorCode::None};
    Severity severity{Severity::Info};

    std::string message;
    std::string component;

    std::string nodeId;
    std::string jobId;
    std::string taskId;

    bool recoverable{false};
    uint64_t timestamp{0};

    Error() = default;

    Error(
        ErrorCode code_,
        Severity severity_,
        std::string message_,
        std::string component_ = "",
        std::string nodeId_ = "",
        std::string jobId_ = "",
        std::string taskId_ = "",
        bool recoverable_ = false,
        uint64_t timestamp_ = 0)
        : code(code_),
          severity(severity_),
          message(std::move(message_)),
          component(std::move(component_)),
          nodeId(std::move(nodeId_)),
          jobId(std::move(jobId_)),
          taskId(std::move(taskId_)),
          recoverable(recoverable_),
          timestamp(timestamp_ == 0 ? static_cast<uint64_t>(std::time(nullptr)) : timestamp_) {}

    bool isFatal() const {
        return !recoverable && severity >= Severity::Error;
    }

    bool isRecoverable() const {
        return recoverable;
    }
};
