#include "native_task_bridge.h"

#include <cstdint>
#include <string>
#include <vector>

#include "../../Core/C/source/memory.c"
#include "../../Core/Cpp/header/source.hpp"
#include "../../Core/Cpp/source/execution_engine.cpp"

extern "C" int64_t crazyapp_asm_sum(const int32_t *values, size_t count) {
    int64_t total = 0;
    for (size_t i = 0; i < count; ++i) {
        total += static_cast<int64_t>(values[i]);
    }
    return total;
}

struct NativeTaskEngine {
    ExecutionEngine engine;
};

extern "C" void *nodren_native_create_engine(void) {
    auto *engine = new NativeTaskEngine();
    if (!engine->engine.initialize()) {
        delete engine;
        return nullptr;
    }
    return engine;
}

extern "C" void nodren_native_destroy_engine(void *engine) {
    if (engine == nullptr) {
        return;
    }
    delete static_cast<NativeTaskEngine *>(engine);
}

extern "C" long long nodren_native_submit_task(void *engine, const char *task_id, const int32_t *payload, size_t payload_len) {
    if (engine == nullptr || payload == nullptr || payload_len == 0) {
        return -1;
    }

    auto *native = static_cast<NativeTaskEngine *>(engine);
    Task task;
    task.id = task_id == nullptr ? "native-task" : std::string(task_id);
    task.type = "sum";
    task.payload.assign(payload, payload + payload_len);
    task.required_memory = payload_len * sizeof(int32_t);

    const TaskResult result = native->engine.submit_task(task);
    if (!result.ok) {
        return -1;
    }

    return result.value;
}
