#![allow(dead_code)]

/// Returns the operating system name.
pub fn name() -> &'static str {
    if cfg!(target_os = "windows") {
        "Windows"
    } else if cfg!(target_os = "linux") {
        "Linux"
    } else if cfg!(target_os = "macos") {
        "macOS"
    } else if cfg!(target_os = "freebsd") {
        "FreeBSD"
    } else if cfg!(target_os = "android") {
        "Android"
    } else if cfg!(target_os = "ios") {
        "iOS"
    } else {
        "Unknown"
    }
}

/// Returns the CPU architecture Nodren was compiled for.
pub fn architecture() -> &'static str {
    if cfg!(target_arch = "x86_64") {
        "x86_64"
    } else if cfg!(target_arch = "aarch64") {
        "aarch64"
    } else if cfg!(target_arch = "x86") {
        "x86"
    } else if cfg!(target_arch = "arm") {
        "ARM"
    } else if cfg!(target_arch = "riscv64") {
        "riscv64"
    } else {
        "Unknown"
    }
}

/// Returns whether Nodren is running on a Unix-like platform.
pub fn is_unix() -> bool {
    cfg!(unix)
}

/// Returns whether Nodren is running on Windows.
pub fn is_windows() -> bool {
    cfg!(windows)
}

/// Returns a short platform identifier useful for logging/API requests.
pub fn identifier() -> &'static str {
    if cfg!(target_os = "windows") {
        "windows"
    } else if cfg!(target_os = "linux") {
        "linux"
    } else if cfg!(target_os = "macos") {
        "macos"
    } else if cfg!(target_os = "freebsd") {
        "freebsd"
    } else if cfg!(target_os = "android") {
        "android"
    } else if cfg!(target_os = "ios") {
        "ios"
    } else {
        "unknown"
    }
}

/// Returns a human-readable platform summary.
pub fn summary() -> String {
    format!("{} ({})", name(), architecture())
}
