#include "../include/core_engine.hpp"
#include "../../C/include/nodren_memory.h"

#include <cstdint>
#include <iostream>
#include <vector>

int main() {
    nodren_memory_init();

    nodren::CoreEngine engine;
    if (!engine.initialize()) {
        std::cerr << "Nodren Core initialization failed\n";
        return 1;
    }

    std::vector<std::int32_t> values(1 << 20);
    for (std::size_t i = 0; i < values.size(); ++i) {
        values[i] = static_cast<std::int32_t>(i & 1023u);
    }

    nodren::Task task{};
    task.id = 1;
    task.payload = values.data();
    task.count = values.size();

    const nodren::TaskResult result = engine.execute_sync(task);

    std::cout
        << "Nodren Core\n"
        << "workers=" << engine.worker_count() << '\n'
        << "hardware_threads=" << engine.cpu().hardware_threads << '\n'
        << "avx2=" << std::boolalpha << engine.cpu().avx2 << '\n'
        << "result=" << result.value << '\n';

    engine.shutdown();
    nodren_memory_shutdown();
    return result.state == nodren::TaskState::Completed ? 0 : 2;
}
