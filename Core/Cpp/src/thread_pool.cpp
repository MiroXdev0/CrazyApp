#include "../include/thread_pool.hpp"

#include <chrono>

namespace nodren {

ThreadPool::ThreadPool(std::size_t worker_count, std::size_t queue_capacity)
    : queue_(queue_capacity) {
    if (worker_count == 0) {
        worker_count = std::thread::hardware_concurrency();
        if (worker_count == 0) worker_count = 1;
    }

    workers_.reserve(worker_count);
    for (std::size_t i = 0; i < worker_count; ++i) {
        workers_.emplace_back(&ThreadPool::worker_loop, this);
    }
}

ThreadPool::~ThreadPool() {
    stopping_.store(true, std::memory_order_release);
    wake_cv_.notify_all();

    for (auto& worker : workers_) {
        if (worker.joinable()) worker.join();
    }
}

bool ThreadPool::submit(const Task& task) noexcept {
    if (stopping_.load(std::memory_order_acquire) || task.fn == nullptr) {
        return false;
    }

    pending_.fetch_add(1, std::memory_order_acq_rel);
    if (!queue_.try_push(task)) {
        pending_.fetch_sub(1, std::memory_order_acq_rel);
        return false;
    }

    wake_cv_.notify_one();
    return true;
}

void ThreadPool::wait_idle() noexcept {
    std::unique_lock<std::mutex> lock(wake_mutex_);
    wake_cv_.wait(lock, [this] {
        return pending_.load(std::memory_order_acquire) == 0;
    });
}

std::size_t ThreadPool::worker_count() const noexcept {
    return workers_.size();
}

std::uint64_t ThreadPool::completed_tasks() const noexcept {
    return completed_.load(std::memory_order_relaxed);
}

void ThreadPool::worker_loop() noexcept {
    for (;;) {
        Task task{};

        if (queue_.try_pop(task)) {
            if (task.fn != nullptr) {
                const std::int64_t value =
                    task.fn(task.payload, task.count, task.user_data);

                if (task.result != nullptr) {
                    *task.result = value;
                }
            }

            pending_.fetch_sub(1, std::memory_order_acq_rel);
            completed_.fetch_add(1, std::memory_order_relaxed);
            wake_cv_.notify_all();
            continue;
        }

        if (stopping_.load(std::memory_order_acquire) &&
            pending_.load(std::memory_order_acquire) == 0) {
            return;
        }

        std::unique_lock<std::mutex> lock(wake_mutex_);
        wake_cv_.wait_for(lock, std::chrono::microseconds(50));
    }
}

} // namespace nodren
