#pragma once

#include <string>
#include <vector>

#include "resource_manager.hpp"
#include "source.hpp"

enum class TaskState {
    CREATED,
    QUEUED,
    RUNNING,
    COMPLETED,
    FAILED,
    CANCELLED
};

struct ScheduledTask {
    Task task;
    TaskState state = TaskState::CREATED;
    int64_t result_value = 0;
    std::string message;
    TaskResult result;
};

class TaskScheduler {
public:
    bool initialize(ExecutionEngine *engine, ResourceManager *resource_manager);
    bool enqueue(const Task &task);
    bool run_next();
    std::vector<ScheduledTask> snapshot() const;
    ResourceSnapshot resources() const;

private:
    ExecutionEngine *engine_ = nullptr;
    ResourceManager *resource_manager_ = nullptr;
    std::vector<ScheduledTask> tasks_;
};
