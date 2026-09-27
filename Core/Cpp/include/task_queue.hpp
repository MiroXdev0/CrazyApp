#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <type_traits>

namespace nodren {

template <typename T>
class MPMCBoundedQueue final {
    static_assert(std::is_trivially_copyable_v<T>,
                  "MPMCBoundedQueue requires trivially copyable items");

    struct Cell {
        std::atomic<std::size_t> sequence;
        T data{};
    };

public:
    explicit MPMCBoundedQueue(std::size_t capacity)
        : capacity_(normalize_capacity(capacity)),
          mask_(capacity_ - 1),
          buffer_(std::make_unique<Cell[]>(capacity_)),
          enqueue_pos_(0),
          dequeue_pos_(0) {
        for (std::size_t i = 0; i < capacity_; ++i) {
            buffer_[i].sequence.store(i, std::memory_order_relaxed);
        }
    }

    bool try_push(const T& value) noexcept {
        Cell* cell;
        std::size_t pos = enqueue_pos_.load(std::memory_order_relaxed);

        for (;;) {
            cell = &buffer_[pos & mask_];
            const std::size_t seq =
                cell->sequence.load(std::memory_order_acquire);
            const intptr_t diff =
                static_cast<intptr_t>(seq) - static_cast<intptr_t>(pos);

            if (diff == 0) {
                if (enqueue_pos_.compare_exchange_weak(
                        pos, pos + 1,
                        std::memory_order_relaxed,
                        std::memory_order_relaxed)) {
                    break;
                }
            } else if (diff < 0) {
                return false;
            } else {
                pos = enqueue_pos_.load(std::memory_order_relaxed);
            }
        }

        cell->data = value;
        cell->sequence.store(pos + 1, std::memory_order_release);
        return true;
    }

    bool try_pop(T& value) noexcept {
        Cell* cell;
        std::size_t pos = dequeue_pos_.load(std::memory_order_relaxed);

        for (;;) {
            cell = &buffer_[pos & mask_];
            const std::size_t seq =
                cell->sequence.load(std::memory_order_acquire);
            const intptr_t diff =
                static_cast<intptr_t>(seq) -
                static_cast<intptr_t>(pos + 1);

            if (diff == 0) {
                if (dequeue_pos_.compare_exchange_weak(
                        pos, pos + 1,
                        std::memory_order_relaxed,
                        std::memory_order_relaxed)) {
                    break;
                }
            } else if (diff < 0) {
                return false;
            } else {
                pos = dequeue_pos_.load(std::memory_order_relaxed);
            }
        }

        value = cell->data;
        cell->sequence.store(pos + mask_ + 1,
                             std::memory_order_release);
        return true;
    }

    std::size_t capacity() const noexcept {
        return capacity_;
    }

private:
    static std::size_t normalize_capacity(std::size_t value) {
        if (value < 2) value = 2;
        std::size_t result = 1;
        while (result < value) result <<= 1;
        return result;
    }

    const std::size_t capacity_;
    const std::size_t mask_;
    std::unique_ptr<Cell[]> buffer_;
    alignas(64) std::atomic<std::size_t> enqueue_pos_;
    alignas(64) std::atomic<std::size_t> dequeue_pos_;
};

} // namespace nodren
