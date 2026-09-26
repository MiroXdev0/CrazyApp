#include "../header/source.hpp"

#include <iostream>
#include <thread>

ExecutionEngine::ExecutionEngine() : arena_(nullptr), arena_size_(4096) {
}

ExecutionEngine::~ExecutionEngine() {
    shutdown();
}

bool ExecutionEngine::initialize() {
    cpu_capabilities_ = detect_cpu();
    arena_ = crazyapp_aligned_alloc(64, arena_size_);
    if (arena_ == nullptr) {
        std::cerr << "[core] failed to allocate engine arena" << std::endl;
        return false;
    }

    std::cout << "[core] initialized memory arena with " << arena_size_ << " bytes" << std::endl;
    return true;
}

CPUCapabilities ExecutionEngine::detect_cpu() const {
    CPUCapabilities caps{};

#if defined(__x86_64__) || defined(_M_X64)
    __builtin_cpu_init();
    caps.sse2 = __builtin_cpu_supports("sse2");
    caps.sse4_2 = __builtin_cpu_supports("sse4.2");
    caps.avx = __builtin_cpu_supports("avx");
    caps.avx2 = __builtin_cpu_supports("avx2");
#endif

    caps.logical_cores = static_cast<int>(std::thread::hardware_concurrency());
    if (caps.logical_cores <= 0) {
        caps.logical_cores = 1;
    }

    return caps;
}

int64_t ExecutionEngine::generic_sum(const std::vector<int32_t>& payload) const {
    int64_t total = 0;
    for (int32_t value : payload) {
        total += static_cast<int64_t>(value);
    }
    return total;
}

int64_t ExecutionEngine::execute_sum(const std::vector<int32_t>& payload) {
    if (payload.empty()) {
        return 0;
    }

    if (cpu_capabilities_.avx2) {
        return crazyapp_asm_sum(payload.data(), payload.size());
    }

    return generic_sum(payload);
}

TaskResult ExecutionEngine::submit_task(const Task& task) {
    TaskResult result;
    result.task_id = task.id;
    result.ok = false;

    if (task.payload.empty()) {
        result.message = "empty payload";
        return result;
    }

    if (arena_ == nullptr) {
        result.message = "execution engine not initialized";
        return result;
    }

    const size_t required_bytes = task.payload.size() * sizeof(int32_t);
    if (required_bytes > arena_size_) {
        result.message = "payload exceeds arena capacity";
        return result;
    }

    if (task.required_memory > 0 && task.required_memory > arena_size_) {
        result.message = "task requires more memory than available";
        return result;
    }

    result.value = execute_sum(task.payload);
    result.ok = true;
    result.message = "task completed";
    return result;
}

void ExecutionEngine::shutdown() {
    if (arena_ != nullptr) {
        crazyapp_free(arena_);
        arena_ = nullptr;
    }
    std::cout << "[core] execution engine shutdown complete" << std::endl;
}
