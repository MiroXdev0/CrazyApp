#include "../include/nodren_c_api.h"
#include "../include/core_engine.hpp"
#include "../include/workload_dispatch.hpp"

#include <new>
#include <string_view>

extern "C" {

void* nodren_core_create(const NodrenCoreConfig* config) {
    std::size_t workers = 0;
    std::size_t queue = 4096;

    if (config) {
        workers = config->worker_count;
        if (config->queue_capacity) queue = config->queue_capacity;
    }

    try {
        return new nodren::CoreEngine(workers, queue);
    } catch (...) {
        return nullptr;
    }
}

int nodren_core_initialize(void* engine) {
    if (!engine) return -1;
    return static_cast<nodren::CoreEngine*>(engine)->initialize() ? 0 : -1;
}

void nodren_core_destroy(void* engine) {
    if (!engine) return;
    delete static_cast<nodren::CoreEngine*>(engine);
}

int nodren_core_submit_sum(
    void* engine,
    uint64_t task_id,
    const int32_t* values,
    size_t count) {
    if (!engine || !values || count == 0) return -1;

    nodren::Task task{};
    task.id = task_id;
    task.payload = values;
    task.count = count;

    return static_cast<nodren::CoreEngine*>(engine)->submit(task) ? 0 : -1;
}

int nodren_core_execute_sum(
    void* engine,
    uint64_t task_id,
    const int32_t* values,
    size_t count,
    NodrenCoreTaskResult* out) {
    if (!engine || !values || count == 0 || !out) return -1;

    nodren::Task task{};
    task.id = task_id;
    task.payload = values;
    task.count = count;

    const nodren::TaskResult result =
        static_cast<nodren::CoreEngine*>(engine)->execute_sync(task);

    out->task_id = result.id;
    out->value = result.value;
    out->state = static_cast<int>(result.state);
    return result.state == nodren::TaskState::Completed ? 0 : -1;
}

int nodren_core_execute_workload(
    void* engine,
    uint64_t task_id,
    const char* command,
    const uint8_t* payload,
    size_t payload_size,
    NodrenCoreWorkloadResult* out) {
    if (!out) return -1;
    out->task_id = task_id;
    out->value = 0;
    out->state = static_cast<int>(nodren::TaskState::Failed);
    out->error_code = NODREN_CORE_ERROR_INVALID_ARGUMENT;
    if (!engine || !command) return -1;

    nodren::WorkloadResult result{};
    try {
        result = nodren::dispatch_workload(
            *static_cast<nodren::CoreEngine*>(engine),
            task_id,
            std::string_view(command),
            payload,
            payload_size);
    } catch (...) {
        out->error_code = NODREN_CORE_ERROR_INTERNAL;
        return -1;
    }
    out->task_id = result.task.id;
    out->value = result.task.value;
    out->state = static_cast<int>(result.task.state);
    out->error_code = static_cast<int>(result.error);
    return result.task.state == nodren::TaskState::Completed ? 0 : -1;
}

void nodren_core_wait_idle(void* engine) {
    if (engine) {
        static_cast<nodren::CoreEngine*>(engine)->wait_idle();
    }
}

} // extern "C"
