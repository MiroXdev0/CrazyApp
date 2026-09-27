#include "error/ErrorManager.hpp"

#include <mutex>
#include <vector>

void ErrorRegistry::add(const Error& error) const {
    std::lock_guard<std::mutex> lock(mutex_);
    entries_.push_back(error);
}

void ErrorRegistry::clear() const {
    std::lock_guard<std::mutex> lock(mutex_);
    entries_.clear();
}

std::vector<Error> ErrorRegistry::snapshot() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return entries_;
}

std::size_t ErrorRegistry::size() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return entries_.size();
}
