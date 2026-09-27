#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <mutex>
#include <queue>
#include <sstream>
#include <string>
#include <thread>
#include <vector>

struct Task {
    int task_id;
    int payload_size;
    int seed;
    std::chrono::steady_clock::time_point enqueue_time;
};

struct TaskResult {
    int task_id;
    std::string node_id;
    uint64_t result;
    double compute_ms;
    double queue_wait_ms;
    double result_serialization_ms;
};

template <typename T>
class BlockingQueue {
public:
    void push(const T& item) {
        std::lock_guard<std::mutex> lock(mutex_);
        queue_.push(item);
        condition_.notify_one();
    }

    bool pop(T& item) {
        std::unique_lock<std::mutex> lock(mutex_);
        condition_.wait(lock, [this] { return !queue_.empty() || stop_; });
        if (queue_.empty()) {
            return false;
        }
        item = queue_.front();
        queue_.pop();
        return true;
    }

    void stop() {
        std::lock_guard<std::mutex> lock(mutex_);
        stop_ = true;
        condition_.notify_all();
    }

private:
    std::queue<T> queue_;
    std::mutex mutex_;
    std::condition_variable condition_;
    bool stop_ = false;
};

static uint64_t deterministic_value(uint64_t seed, uint64_t idx) {
    uint64_t x = seed + idx * 131 + (idx % 17) * 7;
    x ^= x << 13;
    x ^= x >> 7;
    x *= 0x9E3779B97F4A7C15ULL;
    return x;
}

static uint64_t compute_task(int task_id, int payload_size) {
    uint64_t total = 0;
    for (int i = 0; i < payload_size; ++i) {
        total += deterministic_value(static_cast<uint64_t>(task_id + 17), static_cast<uint64_t>(i));
    }
    return total;
}

static std::string trim_crlf(const std::string& value) {
    std::string out = value;
    while (!out.empty() && (out.back() == '\r' || out.back() == '\n')) {
        out.pop_back();
    }
    return out;
}

static bool parse_int(const std::string& value, int& out) {
    std::istringstream stream(value);
    stream >> out;
    return !stream.fail() && stream.eof();
}

static std::vector<std::string> split_batch(const std::string& line) {
    std::vector<std::string> out;
    std::string current;
    for (char ch : line) {
        if (ch == ';') {
            if (!current.empty()) {
                out.push_back(current);
            }
            current.clear();
        } else {
            current.push_back(ch);
        }
    }
    if (!current.empty()) {
        out.push_back(current);
    }
    return out;
}

static std::vector<Task> parse_batch_tasks(const std::string& line) {
    std::vector<Task> tasks;
    for (const std::string& item : split_batch(line)) {
        std::istringstream stream(item);
        std::string part;
        std::string fields[3];
        int idx = 0;
        while (std::getline(stream, part, '|') && idx < 3) {
            fields[idx++] = trim_crlf(part);
        }
        if (idx != 3) {
            continue;
        }
        int task_id = 0;
        int payload_size = 0;
        int seed = 0;
        if (!parse_int(fields[0], task_id) || !parse_int(fields[1], payload_size) || !parse_int(fields[2], seed)) {
            continue;
        }
        tasks.push_back(Task{task_id, payload_size, seed, std::chrono::steady_clock::now()});
    }
    return tasks;
}

static void run_single_task(const std::string& node_id, int task_id, int payload_size) {
    auto start = std::chrono::steady_clock::now();
    uint64_t result = compute_task(task_id, payload_size);
    auto end = std::chrono::steady_clock::now();
    double compute_ms = std::chrono::duration<double, std::milli>(end - start).count();
    double result_serialization_ms = 0.0;
    auto result_start = std::chrono::steady_clock::now();
    std::cout << "{\"task_id\":" << task_id << ",\"node_id\":\"" << node_id
              << "\",\"result\":" << result << ",\"compute_ms\":" << compute_ms
              << ",\"queue_wait_ms\":0.0,\"result_serialization_ms\":" << result_serialization_ms
              << ",\"round_trip_ms\":0.0}\n";
    std::cout.flush();
    (void)result_start;
}

static void worker_loop(const std::string& node_id, BlockingQueue<Task>& task_queue, BlockingQueue<TaskResult>& result_queue, int worker_index) {
    (void)worker_index;
    while (true) {
        Task task;
        if (!task_queue.pop(task)) {
            return;
        }
        auto queue_ready = std::chrono::steady_clock::now();
        auto start = std::chrono::steady_clock::now();
        uint64_t result = compute_task(task.task_id, task.payload_size);
        auto end = std::chrono::steady_clock::now();
        double compute_ms = std::chrono::duration<double, std::milli>(end - start).count();
        double queue_wait_ms = std::chrono::duration<double, std::milli>(queue_ready - task.enqueue_time).count();
        auto result_start = std::chrono::steady_clock::now();
        TaskResult task_result{task.task_id, node_id, result, compute_ms, queue_wait_ms, 0.0};
        double result_serialization_ms = std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - result_start).count();
        task_result.result_serialization_ms = result_serialization_ms;
        result_queue.push(task_result);
    }
}

static void stdin_reader(BlockingQueue<Task>& task_queue) {
    while (true) {
        std::string line;
        if (!std::getline(std::cin, line)) {
            break;
        }
        line = trim_crlf(line);
        if (line == "EOF") {
            break;
        }
        std::vector<Task> tasks = parse_batch_tasks(line);
        for (Task& task : tasks) {
            task.enqueue_time = std::chrono::steady_clock::now();
            task_queue.push(task);
        }
    }
    task_queue.stop();
}

static void stdout_writer(BlockingQueue<TaskResult>& result_queue) {
    while (true) {
        TaskResult result;
        if (!result_queue.pop(result)) {
            return;
        }
        double result_serialization_ms = 0.0;
        auto begin = std::chrono::steady_clock::now();
        std::cout << "{\"task_id\":" << result.task_id
                  << ",\"node_id\":\"" << result.node_id
                  << "\",\"result\":" << result.result
                  << ",\"compute_ms\":" << result.compute_ms
                  << ",\"queue_wait_ms\":" << result.queue_wait_ms
                  << ",\"result_serialization_ms\":" << result_serialization_ms
                  << ",\"round_trip_ms\":0.0}\n";
        std::cout.flush();
        (void)begin;
    }
}

int main(int argc, char** argv) {
    std::string node_id = "node-0";
    int task_id = 0;
    int payload_size = 4096;
    int worker_count = 1;
    std::string mode = "task";

    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "--mode=node") {
            mode = "node";
        } else if (arg == "--mode=task") {
            mode = "task";
        } else if (arg.rfind("--node-id=", 0) == 0) {
            node_id = arg.substr(strlen("--node-id="));
        } else if (arg.rfind("--task-id=", 0) == 0) {
            int parsed_value = 0;
            if (parse_int(arg.substr(strlen("--task-id=")), parsed_value)) {
                task_id = parsed_value;
            }
        } else if (arg.rfind("--payload-size=", 0) == 0) {
            int parsed_value = 0;
            if (parse_int(arg.substr(strlen("--payload-size=")), parsed_value)) {
                payload_size = parsed_value;
            }
        } else if (arg.rfind("--workers=", 0) == 0) {
            int parsed_value = 0;
            if (parse_int(arg.substr(strlen("--workers=")), parsed_value)) {
                worker_count = parsed_value;
            }
        }
    }

    if (mode == "node") {
        std::cout << "{\"status\":\"ready\",\"node_id\":\"" << node_id << "\"}\n";
        std::cout.flush();

        BlockingQueue<Task> task_queue;
        BlockingQueue<TaskResult> result_queue;
        std::vector<std::thread> workers;
        for (int i = 0; i < worker_count; ++i) {
            workers.emplace_back(worker_loop, node_id, std::ref(task_queue), std::ref(result_queue), i);
        }

        std::thread reader(stdin_reader, std::ref(task_queue));
        std::thread writer(stdout_writer, std::ref(result_queue));
        reader.join();
        for (auto& worker : workers) {
            worker.join();
        }
        result_queue.stop();
        writer.join();
        return 0;
    }

    run_single_task(node_id, task_id, payload_size);
    return 0;
}
