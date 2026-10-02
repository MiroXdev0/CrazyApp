#include "../include/core_engine.hpp"
#include "../include/compute_kernel.hpp"
#include "../include/workload_dispatch.hpp"

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

    if (engine.parallel_sum_i32(values.data(), values.size()) != expected) {
        std::cerr << "parallel sum mismatch\n";
        return 3;
    }
    std::int64_t expected_xor = 0;
    for (const auto value : values) expected_xor ^= value;
    if (engine.parallel_xor_i32(values.data(), values.size()) != expected_xor) {
        std::cerr << "parallel xor mismatch\n";
        return 3;
    }
    std::vector<std::int32_t> rhs(values.size(), 2);
    if (engine.parallel_dot_product_i32(values.data(), rhs.data(), values.size()) != expected * 2) {
        std::cerr << "parallel dot mismatch\n";
        return 3;
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

    const std::vector<std::uint8_t> sum_payload{1, 2, 3, 4, 5};
    const auto sum = nodren::dispatch_workload(
        engine, 1001, "sum", sum_payload.data(), sum_payload.size());
    if (sum.error != nodren::WorkloadError::None ||
        sum.task.state != nodren::TaskState::Completed || sum.task.value != 15) {
        std::cerr << "sum dispatch mismatch\n";
        return 5;
    }

    std::vector<std::uint8_t> dot_payload;
    const auto append_u32 = [&dot_payload](std::uint32_t value) {
        for (unsigned shift = 0; shift < 32; shift += 8) {
            dot_payload.push_back(static_cast<std::uint8_t>(value >> shift));
        }
    };
    const auto append_i32 = [&append_u32](std::int32_t value) {
        append_u32(static_cast<std::uint32_t>(value));
    };
    append_u32(3);
    for (const auto value : {1, 2, 3}) append_i32(value);
    for (const auto value : {4, 5, 6}) append_i32(value);

    const auto dot = nodren::dispatch_workload(
        engine, 1002, "dot_product", dot_payload.data(), dot_payload.size());
    if (dot.error != nodren::WorkloadError::None ||
        dot.task.state != nodren::TaskState::Completed || dot.task.value != 32) {
        std::cerr << "dot_product dispatch mismatch\n";
        return 6;
    }

    const auto unsupported = nodren::dispatch_workload(
        engine, 1003, "unknown_workload", sum_payload.data(), sum_payload.size());
    if (unsupported.error != nodren::WorkloadError::UnsupportedWorkload ||
        unsupported.task.state != nodren::TaskState::Failed) {
        std::cerr << "unsupported workload was not rejected\n";
        return 7;
    }

    const std::uint8_t malformed[] = {3, 0};
    const auto malformed_result = nodren::dispatch_workload(
        engine, 1004, "dot_product", malformed, sizeof(malformed));
    if (malformed_result.error != nodren::WorkloadError::MalformedPayload ||
        malformed_result.task.state != nodren::TaskState::Failed) {
        std::cerr << "malformed workload was not rejected\n";
        return 8;
    }

    engine.shutdown();
    std::cout << "Nodren Core self-test: PASS\n";
    return 0;
}
