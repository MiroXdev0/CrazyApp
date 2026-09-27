#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <condition_variable>
#include <deque>
#include <functional>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

struct PerformanceMetrics {
    std::size_t tasks_completed = 0;
    double tasks_per_second = 0.0;
    double operations_per_second = 0.0;
    double cpu_utilization = 0.0;
    double average_latency_ms = 0.0;
    double memory_bandwidth_gbps = 0.0;
    double scaling_efficiency = 0.0;
};

struct BatchTask {
    std::string id;
    std::vector<int32_t> payload;
    int priority = 0;
};

class MemoryArena {
public:
    explicit MemoryArena(std::size_t bytes);
    ~MemoryArena();

    void* allocate(std::size_t bytes);
    void reset();
    std::size_t capacity() const;

private:
    std::vector<std::byte> storage_;
    std::size_t offset_ = 0;
};

class ThreadPool {
public:
    explicit ThreadPool(std::size_t threads);
    ~ThreadPool();

    template <typename F>
    void enqueue(F&& job) {
        {
            std::lock_guard<std::mutex> lock(queue_mutex_);
            tasks_.emplace_back(std::forward<F>(job));
        }
        condition_.notify_one();
    }

private:
    void workerLoop();

    mutable std::mutex queue_mutex_;
    std::condition_variable condition_;
    std::deque<std::function<void()>> tasks_;
    std::vector<std::thread> workers_;
    bool stop_ = false;
};

class PerformanceScheduler {
public:
    explicit PerformanceScheduler(std::size_t threads = 4, std::size_t arena_bytes = 1 << 20);
    ~PerformanceScheduler();

    void enqueue(const BatchTask& task);
    PerformanceMetrics runBenchmark(std::size_t task_count, std::size_t batch_size);
    std::size_t queueDepth() const;

private:
    ThreadPool pool_;
    MemoryArena arena_;
    mutable std::mutex queue_mutex_;
    std::deque<BatchTask> pending_;
    std::atomic<std::size_t> completed_{0};
};
