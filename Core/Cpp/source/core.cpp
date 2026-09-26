#include "../header/source.hpp"
#include "../header/resource_manager.hpp"
#include "../header/task_system.hpp"

#include <iostream>

int main() {
    crazyapp_memory_init();

    ExecutionEngine engine;
    if (!engine.initialize()) {
        std::cerr << "[core] initialize failed" << std::endl;
        return 1;
    }

    ResourceManager resource_manager;
    ResourceSnapshot resources = resource_manager.query();
    TaskScheduler scheduler;
    scheduler.initialize(&engine, &resource_manager);

    CPUCapabilities caps = engine.detect_cpu();
    std::cout << "[core] x86-64 CPU capabilities" << std::endl;
    std::cout << "[core] SSE2=" << std::boolalpha << caps.sse2
              << " SSE4.2=" << caps.sse4_2
              << " AVX=" << caps.avx
              << " AVX2=" << caps.avx2
              << " cores=" << caps.logical_cores << std::endl;
    std::cout << "[core] resources: logical_threads=" << resources.logical_threads
              << " total_ram_mb=" << resources.total_ram_mb
              << " available_ram_mb=" << resources.available_ram_mb
              << " platform=" << resources.platform << std::endl;

    Task task{
        "task-0001",
        "sum",
        {4, 8, 15, 16, 23, 42, 9, 3},
        256,
        5
    };

    scheduler.enqueue(task);
    if (!scheduler.run_next()) {
        std::cerr << "[core] scheduler failed to execute task" << std::endl;
        engine.shutdown();
        crazyapp_memory_shutdown();
        return 2;
    }

    std::vector<ScheduledTask> scheduled = scheduler.snapshot();
    if (!scheduled.empty()) {
        const ScheduledTask &completed = scheduled.front();
        std::cout << "[core] task " << completed.task.id << " value=" << completed.result_value
                  << " state=" << (completed.state == TaskState::COMPLETED ? "COMPLETED" : "UNKNOWN")
                  << std::endl;
    }

    engine.shutdown();
    crazyapp_memory_shutdown();
    std::cout << "[core] shutdown complete" << std::endl;
    return 0;
}
