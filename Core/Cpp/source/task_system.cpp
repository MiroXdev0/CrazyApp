#include "../header/task_system.hpp"

#include <vector>

bool TaskScheduler::initialize(ExecutionEngine *engine, ResourceManager *resource_manager) {
    engine_ = engine;
    resource_manager_ = resource_manager;
    return engine_ != nullptr;
}

bool TaskScheduler::enqueue(const Task &task) {
    ScheduledTask entry;
    entry.task = task;
    entry.state = TaskState::QUEUED;
    entry.message = "queued";
    tasks_.push_back(entry);
    return true;
}

bool TaskScheduler::run_next() {
    if (tasks_.empty()) {
        return false;
    }

    ScheduledTask &entry = tasks_.front();
    entry.state = TaskState::RUNNING;
    entry.message = "running";

    if (engine_ == nullptr) {
        entry.state = TaskState::FAILED;
        entry.message = "engine unavailable";
        return false;
    }

    entry.result = engine_->submit_task(entry.task);
    if (!entry.result.ok) {
        entry.state = TaskState::FAILED;
        entry.message = entry.result.message;
        return false;
    }

    entry.result_value = entry.result.value;
    entry.state = TaskState::COMPLETED;
    entry.message = entry.result.message;
    return true;
}

std::vector<ScheduledTask> TaskScheduler::snapshot() const {
    return tasks_;
}

ResourceSnapshot TaskScheduler::resources() const {
    if (resource_manager_ == nullptr) {
        return ResourceSnapshot{};
    }
    return resource_manager_->query();
}
