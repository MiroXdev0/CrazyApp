#pragma once

#include "core_types.hpp"
#include "task_queue.hpp"

#include <atomic>
#include <condition_variable>
#include <cstddef>
#include <mutex>
#include <thread>
#include <vector>

namespace nodren {

class ThreadPool final {
public:
    explicit ThreadPool(std::size_t worker_count, std::size_t queue_capacity = 4096);
    ~ThreadPool();

    ThreadPool(const ThreadPool&) = delete;
    ThreadPool& operator=(const ThreadPool&) = delete;

    bool submit(const Task& task) noexcept;
    void wait_idle() noexcept;

    std::size_t worker_count() const noexcept;
    std::uint64_t completed_tasks() const noexcept;

private:
    void worker_loop() noexcept;

    MPMCBoundedQueue<Task> queue_;
    std::vector<std::thread> workers_;

    std::atomic<bool> stopping_{false};
    std::atomic<std::size_t> pending_{0};
    std::atomic<std::uint64_t> completed_{0};

    std::mutex wake_mutex_;
    std::condition_variable wake_cv_;
};

} // namespace nodren
