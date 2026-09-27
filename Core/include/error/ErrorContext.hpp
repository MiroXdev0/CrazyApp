#pragma once

#include <string>

struct ErrorContext {
    std::string component;
    std::string nodeId;
    std::string jobId;
    std::string taskId;
    std::string operation;
    std::string details;

    ErrorContext() = default;

    ErrorContext(
        std::string component_,
        std::string nodeId_ = "",
        std::string jobId_ = "",
        std::string taskId_ = "",
        std::string operation_ = "",
        std::string details_ = "")
        : component(std::move(component_)),
          nodeId(std::move(nodeId_)),
          jobId(std::move(jobId_)),
          taskId(std::move(taskId_)),
          operation(std::move(operation_)),
          details(std::move(details_)) {}
};
