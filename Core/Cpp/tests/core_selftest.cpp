#include "../include/core_engine.hpp"
#include "../include/compute_kernel.hpp"

#include <cstdint>
#include <iostream>
#include <vector>

static std::int64_t expected_sum(const std::vector<std::int32_t>& values) {
    std::int64_t total = 0;
    for (auto value : values) total += value;
    return total;
}

int main() {
    std::vector<std::int32_t> values(1 << 20);
    for (std::size_t i = 0; i < values.size(); ++i) {
        values[i] = static_cast<std::int32_t>((i % 2047) - 1023);
    }

    const auto expected = expected_sum(values);

    if (nodren::scalar_sum_i32(values.data(), values.size()) != expected ||
        nodren::sum_i32(values.data(), values.size()) != expected) {
        std::cerr << "kernel mismatch\n";
        return 1;
    }

    nodren::CoreEngine engine(2, 1024);
    if (!engine.initialize()) {
        std::cerr << "engine init failed\n";
        return 2;
    }

    constexpr std::size_t task_count = 256;
    std::vector<std::int64_t> outputs(task_count);

    for (std::size_t i = 0; i < task_count; ++i) {
        nodren::Task task{};
        task.id = static_cast<nodren::TaskId>(i + 1);
        task.payload = values.data();
        task.count = values.size();
        task.result = &outputs[i];

        if (!engine.submit(task)) {
            std::cerr << "submit failed at " << i << '\n';
            return 3;
        }
    }

    engine.wait_idle();

    for (std::size_t i = 0; i < task_count; ++i) {
        if (outputs[i] != expected) {
            std::cerr << "async result mismatch at " << i << '\n';
            return 4;
        }
    }

    engine.shutdown();
    std::cout << "Nodren Core self-test: PASS\n";
    return 0;
}
