use std::process::Command;

#[derive(Debug, Clone)]
pub struct ProcessInfo {
    pub pid: u32,
    pub name: String,
}

const NODREN_NAMES: &[&str] = &[
    "nodren",
    "nodren.exe",
];

/// Returns all currently running Nodren processes.
pub fn nodren_processes() -> Vec<ProcessInfo> {
    #[cfg(target_os = "windows")]
    {
        windows_processes()
    }

    #[cfg(not(target_os = "windows"))]
    {
        unix_processes()
    }
}

/// Returns the number of currently running Nodren processes.
pub fn count_nodren_processes() -> usize {
    nodren_processes().len()
}

/// Returns true if at least one Nodren process is running.
pub fn is_nodren_running() -> bool {
    !nodren_processes().is_empty()
}

/// Returns true if a specific process name is a Nodren process.
fn is_nodren_process(name: &str) -> bool {
    let name = name.trim();

    NODREN_NAMES
        .iter()
        .any(|expected| name.eq_ignore_ascii_case(expected))
}

// ---------------------------------------------------------
// Windows
// ---------------------------------------------------------

#[cfg(target_os = "windows")]
fn windows_processes() -> Vec<ProcessInfo> {
    let output = Command::new("tasklist")
        .args([
            "/FO",
            "CSV",
            "/NH",
        ])
        .output();

    let Ok(output) = output else {
        return Vec::new();
    };

    if !output.status.success() {
        return Vec::new();
    }

    let text = String::from_utf8_lossy(&output.stdout);

    text.lines()
        .filter_map(parse_windows_process)
        .collect()
}

#[cfg(target_os = "windows")]
fn parse_windows_process(line: &str) -> Option<ProcessInfo> {
    let fields: Vec<&str> = line
        .split(',')
        .map(|field| field.trim_matches('"'))
        .collect();

    if fields.len() < 2 {
        return None;
    }

    let name = fields[0];

    if !is_nodren_process(name) {
        return None;
    }

    let pid = fields[1].parse::<u32>().ok()?;

    Some(ProcessInfo {
        pid,
        name: name.to_string(),
    })
}

// ---------------------------------------------------------
// Unix / Linux / macOS
// ---------------------------------------------------------

#[cfg(not(target_os = "windows"))]
fn unix_processes() -> Vec<ProcessInfo> {
    let output = Command::new("ps")
        .args([
            "-A",
            "-o",
            "pid=",
            "-o",
            "comm=",
        ])
        .output();

    let Ok(output) = output else {
        return Vec::new();
    };

    if !output.status.success() {
        return Vec::new();
    }

    let text = String::from_utf8_lossy(&output.stdout);

    text.lines()
        .filter_map(parse_unix_process)
        .collect()
}

#[cfg(not(target_os = "windows"))]
fn parse_unix_process(line: &str) -> Option<ProcessInfo> {
    let mut parts = line.split_whitespace();

    let pid = parts.next()?.parse::<u32>().ok()?;
    let name = parts.next()?;

    if !is_nodren_process(name) {
        return None;
    }

    Some(ProcessInfo {
        pid,
        name: name.to_string(),
    })
}