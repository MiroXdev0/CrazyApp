#include "error/ErrorManager.hpp"

#include <iostream>
#include <string>

namespace {
ErrorCode toErrorCode(int value) {
    switch (value) {
        case 1: return ErrorCode::NetworkFailure;
        case 2: return ErrorCode::NodeDisconnected;
        case 3: return ErrorCode::ProtocolError;
        case 4: return ErrorCode::InvalidJob;
        case 5: return ErrorCode::InvalidTask;
        case 6: return ErrorCode::TaskExecutionFailed;
        case 7: return ErrorCode::MemoryAllocationFailed;
        case 8: return ErrorCode::ResourceUnavailable;
        case 9: return ErrorCode::CoreFailure;
        default: return ErrorCode::Unknown;
    }
}

Severity toSeverity(int level) {
    switch (level) {
        case 0: return Severity::Trace;
        case 1: return Severity::Info;
        case 2: return Severity::Warning;
        case 3: return Severity::Error;
        case 4: return Severity::Critical;
        default: return Severity::Error;
    }
}

bool isRecoverable(ErrorCode code) {
    switch (code) {
        case ErrorCode::NetworkFailure:
        case ErrorCode::NodeDisconnected:
        case ErrorCode::ProtocolError:
        case ErrorCode::InvalidTask:
        case ErrorCode::TaskExecutionFailed:
        case ErrorCode::MemoryAllocationFailed:
        case ErrorCode::ResourceUnavailable:
            return true;
        default:
            return false;
    }
}

std::string safeMessage(const char* message) {
    return message == nullptr ? std::string("unknown error") : std::string(message);
}
}  // namespace

ErrorManager& ErrorManager::instance() {
    static ErrorManager manager;
    return manager;
}

Error ErrorManager::createError(
    ErrorCode code,
    Severity severity,
    const std::string& message,
    const ErrorContext& context) const {
    Error error(
        code,
        severity,
        message,
        context.component,
        context.nodeId,
        context.jobId,
        context.taskId,
        isRecoverable(code));

    if (severity >= Severity::Error && !error.recoverable) {
        error.recoverable = false;
    }

    return error;
}

Error ErrorManager::classifyError(
    ErrorCode code,
    const std::string& message,
    const std::string& component,
    const ErrorContext& context) const {
    Severity severity = Severity::Error;
    switch (code) {
        case ErrorCode::NetworkFailure:
        case ErrorCode::NodeDisconnected:
        case ErrorCode::ProtocolError:
            severity = Severity::Warning;
            break;
        case ErrorCode::InvalidJob:
        case ErrorCode::InvalidTask:
            severity = Severity::Error;
            break;
        case ErrorCode::TaskExecutionFailed:
        case ErrorCode::MemoryAllocationFailed:
        case ErrorCode::ResourceUnavailable:
            severity = Severity::Critical;
            break;
        case ErrorCode::CoreFailure:
            severity = Severity::Critical;
            break;
        case ErrorCode::Unknown:
        default:
            severity = Severity::Error;
            break;
    }

    ErrorContext effective = context;
    if (!component.empty() && effective.component.empty()) {
        effective.component = component;
    }

    return createError(code, severity, message, effective);
}

void ErrorManager::logError(const Error& error) const {
    std::lock_guard<std::mutex> lock(mutex_);
    registry_.add(error);
    if (error.severity >= Severity::Error) {
        std::cerr << "[nodren-error] code=" << static_cast<int>(error.code)
                  << " severity=" << static_cast<int>(error.severity)
                  << " component=" << (error.component.empty() ? "unknown" : error.component)
                  << " message=" << error.message << '\n';
    }
}

Error ErrorManager::propagateError(
    const Error& original,
    const std::string& component,
    const std::string& details) const {
    std::string propagated = original.message;
    if (!details.empty()) {
        propagated += " | detail: " + details;
    }
    if (!component.empty()) {
        propagated += " | propagated by: " + component;
    }

    Error propagatedError(
        original.code,
        original.severity,
        propagated,
        component.empty() ? original.component : component,
        original.nodeId,
        original.jobId,
        original.taskId,
        original.recoverable,
        original.timestamp);

    return propagatedError;
}

bool ErrorManager::recover(const Error& error) const {
    if (error.recoverable) {
        return true;
    }
    return false;
}

bool ErrorManager::retry(const Error& error) const {
    switch (error.code) {
        case ErrorCode::NetworkFailure:
        case ErrorCode::NodeDisconnected:
        case ErrorCode::ProtocolError:
        case ErrorCode::TaskExecutionFailed:
        case ErrorCode::ResourceUnavailable:
            return true;
        default:
            return false;
    }
}

bool ErrorManager::reschedule(const Error& error) const {
    switch (error.code) {
        case ErrorCode::NodeDisconnected:
        case ErrorCode::ResourceUnavailable:
        case ErrorCode::TaskExecutionFailed:
            return true;
        default:
            return false;
    }
}

std::vector<Error> ErrorManager::recentErrors() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return registry_.snapshot();
}

void ErrorManager::clear() const {
    std::lock_guard<std::mutex> lock(mutex_);
    registry_.clear();
}

void ErrorManager::reportFromC(int code, int severity, const char* message) {
    ErrorManager::instance().logError(
        ErrorManager::instance().classifyError(
            toErrorCode(code),
            safeMessage(message),
            "C ABI",
            ErrorContext("C ABI", "", "", "", "reportFromC", safeMessage(message))));
}

extern "C" void nodren_error_report(int code, int severity, const char* message) {
    ErrorManager::reportFromC(code, severity, message);
}
