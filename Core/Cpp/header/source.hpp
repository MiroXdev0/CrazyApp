#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

extern "C" {
#include "../../C/header/memory.h"
}

extern "C" int64_t crazyapp_asm_sum(const int32_t *values, size_t count);

struct CPUCapabilities {
    bool sse2 = false;
    bool sse4_2 = false;
    bool avx = false;
    bool avx2 = false;
    int logical_cores = 1;
};

struct Task {
    std::string id;
    std::string type;
    std::vector<int32_t> payload;
    size_t required_memory = 0;
    int priority = 0;
};

struct TaskResult {
    std::string task_id;
    int64_t value = 0;
    bool ok = false;
    std::string message;
};

class ExecutionEngine {
public:
    ExecutionEngine();
    ~ExecutionEngine();

    bool initialize();
    CPUCapabilities detect_cpu() const;
    TaskResult submit_task(const Task &task);
    int64_t execute_sum(const std::vector<int32_t> &payload);
    void shutdown();

private:
    int64_t generic_sum(const std::vector<int32_t> &payload) const;

    CPUCapabilities cpu_capabilities_{};
    void *arena_ = nullptr;
    size_t arena_size_ = 4096;
};
