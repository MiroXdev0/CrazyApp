#pragma once

#include <cstddef>
#include <mutex>
#include <string>
#include <vector>

#include "Error.hpp"
#include "ErrorContext.hpp"

class ErrorRegistry {
public:
    void add(const Error& error) const;
    void clear() const;
    std::vector<Error> snapshot() const;
    std::size_t size() const;

private:
    mutable std::vector<Error> entries_;
    mutable std::mutex mutex_;
};

class ErrorManager {
public:
    static ErrorManager& instance();

    Error createError(
        ErrorCode code,
        Severity severity,
        const std::string& message,
        const ErrorContext& context = ErrorContext()) const;

    Error classifyError(
        ErrorCode code,
        const std::string& message,
        const std::string& component = "",
        const ErrorContext& context = ErrorContext()) const;

    void logError(const Error& error) const;

    Error propagateError(
        const Error& original,
        const std::string& component,
        const std::string& details = "") const;

    bool recover(const Error& error) const;
    bool retry(const Error& error) const;
    bool reschedule(const Error& error) const;

    std::vector<Error> recentErrors() const;
    void clear() const;

    // C ABI hook for non-C++ callers.
    static void reportFromC(int code, int severity, const char* message);

private:
    mutable std::mutex mutex_;
    ErrorRegistry registry_;
};

extern "C" {
void nodren_error_report(int code, int severity, const char* message);
}
