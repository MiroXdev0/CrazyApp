use crate::protocol::{GeneralTaskEnvelope, GeneralTaskResult};

use std::{
    collections::HashMap,
    fs::{self, File},
    io::{self, Read, Write},
    path::{Path, PathBuf},
    process::{Child, Command, ExitStatus, Stdio},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

fn capture_output<R: Read + Send + 'static>(
    mut reader: R,
    limit: u64,
) -> thread::JoinHandle<(Vec<u8>, bool)> {
    thread::spawn(move || {
        let limit = usize::try_from(limit.min(64 * 1024 * 1024)).unwrap_or(64 * 1024 * 1024);
        let mut value = Vec::new();
        let mut buffer = vec![0u8; 8192];
        let mut truncated = false;
        loop {
            match reader.read(&mut buffer) {
                Ok(0) => break,
                Ok(size) => {
                    if value.len() < limit {
                        let remaining = limit - value.len();
                        value.extend_from_slice(&buffer[..size.min(remaining)]);
                    }
                    if value.len() >= limit && size > limit.saturating_sub(value.len()) {
                        truncated = true;
                    }
                }
                Err(_) => break,
            }
        }
        (value, truncated)
    })
}

fn runtime_program(runtime: &str) -> Result<String, String> {
    let normalized = runtime.trim().to_ascii_lowercase();
    if normalized.is_empty() {
        return Err("runtime is required".to_string());
    }
    let supported = [
        "python",
        "python3",
        "node",
        "nodejs",
        "bash",
        "sh",
        "powershell",
        "pwsh",
    ];
    if supported.iter().any(|candidate| *candidate == normalized)
        || runtime.contains('/')
        || runtime.contains('\\')
    {
        Ok(runtime.to_string())
    } else {
        Err(format!("unsupported runtime: {runtime}"))
    }
}

fn command_for_task(task: &GeneralTaskEnvelope) -> Result<Command, String> {
    let spec = &task.spec;
    let mut command = match spec.task_type.as_str() {
        "PROCESS" | "COMMAND" => Command::new(&spec.executable),
        "SCRIPT" => {
            let runtime = runtime_program(&spec.runtime)?;
            let mut command = Command::new(runtime);
            command.arg(&spec.script);
            command
        }
        other => return Err(format!("unsupported task type: {other}")),
    };
    command.args(&spec.arguments);
    for (key, value) in &spec.environment {
        command.env(key, value);
    }
    if !spec.working_directory.is_empty() {
        let path = std::path::Path::new(&spec.working_directory);
        if !path.is_dir() {
            return Err("working directory does not exist or is not a directory".to_string());
        }
        command.current_dir(path);
    }
    command
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    Ok(command)
}

fn kill_and_wait(child: &mut Child) -> io::Result<Option<ExitStatus>> {
    let _ = child.kill();
    child.wait().map(Some)
}

fn prepare_input_artifacts(
    task: &mut GeneralTaskEnvelope,
    artifacts: HashMap<String, PathBuf>,
) -> Result<Option<PathBuf>, String> {
    if artifacts.is_empty() {
        return Ok(None);
    }
    let workspace = if task.spec.working_directory.is_empty() {
        let path =
            std::env::temp_dir().join(format!("nodren-task-{}-{}", task.task_id, task.attempt));
        fs::create_dir_all(&path).map_err(|error| error.to_string())?;
        task.spec.working_directory = path.to_string_lossy().into_owned();
        Some(path)
    } else {
        let path = PathBuf::from(&task.spec.working_directory);
        if !path.is_dir() {
            return Err("working directory does not exist or is not a directory".to_string());
        }
        None
    };
    let directory = Path::new(&task.spec.working_directory);
    let result = (|| {
        for artifact in &task.spec.input_artifacts {
            let source = artifacts
                .get(&artifact.name)
                .ok_or_else(|| format!("input artifact {} is not available", artifact.id))?;
            let name = Path::new(&artifact.name);
            if name.file_name().and_then(|value| value.to_str()) != Some(artifact.name.as_str()) {
                return Err(format!("unsafe artifact name: {}", artifact.name));
            }
            if artifact.kind.eq_ignore_ascii_case("package") || artifact.name.ends_with(".tar") {
                extract_tar(source, directory)?;
            } else {
                fs::copy(source, directory.join(name)).map_err(|error| error.to_string())?;
            }
            let _ = fs::remove_file(source);
        }
        Ok(())
    })();
    if let Err(error) = result {
        for source in artifacts.values() {
            let _ = fs::remove_file(source);
        }
        if let Some(path) = workspace.as_ref() {
            let _ = fs::remove_dir_all(path);
        }
        return Err(error);
    }
    Ok(workspace)
}

fn parse_tar_size(value: &[u8]) -> Result<u64, String> {
    let text = value
        .iter()
        .copied()
        .take_while(|value| *value != 0 && *value != b' ')
        .collect::<Vec<_>>();
    if text.is_empty() {
        return Ok(0);
    }
    u64::from_str_radix(
        std::str::from_utf8(&text).map_err(|_| "invalid tar size".to_string())?,
        8,
    )
    .map_err(|_| "invalid tar size".to_string())
}

fn parse_tar_mode(value: &[u8]) -> Result<u32, String> {
    let text = value
        .iter()
        .copied()
        .take_while(|value| *value != 0 && *value != b' ')
        .collect::<Vec<_>>();
    if text.is_empty() {
        return Ok(0);
    }
    u32::from_str_radix(
        std::str::from_utf8(&text).map_err(|_| "invalid tar mode".to_string())?,
        8,
    )
    .map_err(|_| "invalid tar mode".to_string())
}

fn safe_archive_path(value: &str) -> Result<PathBuf, String> {
    if value.is_empty() || value.contains('\\') || value.starts_with('/') || value.contains('\0') {
        return Err("unsafe package path".to_string());
    }
    let path = Path::new(value);
    for component in path.components() {
        if matches!(
            component,
            std::path::Component::ParentDir
                | std::path::Component::CurDir
                | std::path::Component::RootDir
                | std::path::Component::Prefix(_)
        ) {
            return Err("package path traversal rejected".to_string());
        }
    }
    Ok(path.to_path_buf())
}

fn extract_tar(source: &Path, directory: &Path) -> Result<(), String> {
    let mut archive = File::open(source).map_err(|error| error.to_string())?;
    loop {
        let mut header = [0u8; 512];
        archive
            .read_exact(&mut header)
            .map_err(|error| error.to_string())?;
        if header.iter().all(|value| *value == 0) {
            break;
        }
        let name = header[..100]
            .iter()
            .take_while(|value| **value != 0)
            .copied()
            .collect::<Vec<_>>();
        let name = std::str::from_utf8(&name).map_err(|_| "invalid package path".to_string())?;
        let relative = safe_archive_path(name)?;
        let size = parse_tar_size(&header[124..136])?;
        #[cfg(unix)]
        let mode = parse_tar_mode(&header[100..108])?;
        #[cfg(not(unix))]
        let _mode = parse_tar_mode(&header[100..108])?;
        let target = directory.join(&relative);
        match header[156] {
            b'5' => fs::create_dir_all(&target).map_err(|error| error.to_string())?,
            0 | b'0' => {
                if let Some(parent) = target.parent() {
                    fs::create_dir_all(parent).map_err(|error| error.to_string())?;
                }
                let mut output = File::create(&target).map_err(|error| error.to_string())?;
                let mut limited = (&mut archive).take(size);
                io::copy(&mut limited, &mut output).map_err(|error| error.to_string())?;
                if limited.limit() != 0 {
                    return Err("truncated package entry".to_string());
                }
                let padding = (512 - (size % 512)) % 512;
                if padding > 0 {
                    let mut padding_buffer = vec![0u8; padding as usize];
                    archive
                        .read_exact(&mut padding_buffer)
                        .map_err(|error| error.to_string())?;
                }
                #[cfg(unix)]
                if mode != 0 {
                    use std::os::unix::fs::PermissionsExt;
                    fs::set_permissions(&target, fs::Permissions::from_mode(mode & 0o777))
                        .map_err(|error| error.to_string())?;
                }
            }
            _ => return Err("unsupported package entry type".to_string()),
        }
    }
    Ok(())
}

pub fn execute(
    mut task: GeneralTaskEnvelope,
    cancelled: Arc<AtomicBool>,
    input_artifacts: HashMap<String, PathBuf>,
) -> GeneralTaskResult {
    let started = Instant::now();
    let workspace = match prepare_input_artifacts(&mut task, input_artifacts) {
        Ok(workspace) => workspace,
        Err(error) => return failure(task, started, "artifact_prepare_failed", error),
    };
    let mut command = match command_for_task(&task) {
        Ok(command) => command,
        Err(error) => {
            if let Some(path) = workspace {
                let _ = fs::remove_dir_all(path);
            }
            return failure(task, started, "invalid_task", error);
        }
    };
    let mut child = match command.spawn() {
        Ok(child) => child,
        Err(error) => {
            if let Some(path) = workspace {
                let _ = fs::remove_dir_all(path);
            }
            return failure(task, started, "executable_not_found", error.to_string());
        }
    };
    let stdout_reader = child.stdout.take();
    let stderr_reader = child.stderr.take();
    let stdout_thread =
        stdout_reader.map(|reader| capture_output(reader, task.spec.stdout_limit_bytes));
    let stderr_thread =
        stderr_reader.map(|reader| capture_output(reader, task.spec.stderr_limit_bytes));
    let stdin_data = task.spec.stdin.clone();
    let stdin_thread = child.stdin.take().map(|mut stdin| {
        thread::spawn(move || {
            if !stdin_data.is_empty() {
                let _ = stdin.write_all(&stdin_data);
            }
        })
    });
    let status;
    let mut error_code = String::new();
    let mut error = String::new();
    let mut exit_code = None;
    loop {
        if cancelled.load(Ordering::Acquire) {
            let _ = kill_and_wait(&mut child);
            status = "CANCELLED";
            error_code = "cancelled".to_string();
            error = "task cancellation requested".to_string();
            break;
        }
        if task.spec.timeout_ms > 0
            && started.elapsed() >= Duration::from_millis(task.spec.timeout_ms)
        {
            let _ = kill_and_wait(&mut child);
            status = "TIMED_OUT";
            error_code = "timeout".to_string();
            error = format!("task exceeded timeout of {} ms", task.spec.timeout_ms);
            break;
        }
        match child.try_wait() {
            Ok(Some(process_status)) => {
                exit_code = process_status.code();
                if process_status.success() {
                    status = "COMPLETED";
                } else {
                    status = "FAILED";
                    error_code = "nonzero_exit".to_string();
                    error = format!("process exited with status {process_status}");
                }
                break;
            }
            Ok(None) => thread::sleep(Duration::from_millis(20)),
            Err(process_error) => {
                let _ = kill_and_wait(&mut child);
                status = "FAILED";
                error_code = "wait_failed".to_string();
                error = process_error.to_string();
                break;
            }
        }
    }
    let (stdout, stdout_truncated) = stdout_thread
        .and_then(|thread| thread.join().ok())
        .unwrap_or_default();
    let (stderr, stderr_truncated) = stderr_thread
        .and_then(|thread| thread.join().ok())
        .unwrap_or_default();
    if let Some(thread) = stdin_thread {
        let _ = thread.join();
    }
    let result = GeneralTaskResult {
        task_id: task.task_id,
        job_id: task.job_id,
        attempt: task.attempt,
        status: status.to_string(),
        exit_code,
        stdout,
        stderr,
        stdout_truncated,
        stderr_truncated,
        duration_us: started.elapsed().as_micros() as u64,
        error_code,
        error,
    };
    if let Some(path) = workspace {
        let _ = fs::remove_dir_all(path);
    }
    result
}

fn failure(
    task: GeneralTaskEnvelope,
    started: Instant,
    code: &str,
    message: String,
) -> GeneralTaskResult {
    GeneralTaskResult {
        task_id: task.task_id,
        job_id: task.job_id,
        attempt: task.attempt,
        status: "FAILED".to_string(),
        exit_code: None,
        stdout: Vec::new(),
        stderr: Vec::new(),
        stdout_truncated: false,
        stderr_truncated: false,
        duration_us: started.elapsed().as_micros() as u64,
        error_code: code.to_string(),
        error: message,
    }
}

#[cfg(test)]
mod tests {
    use super::execute;
    use crate::protocol::{GeneralTaskEnvelope, GeneralTaskSpec};
    use std::sync::{Arc, atomic::AtomicBool};

    fn task(executable: &str, arguments: &[&str]) -> GeneralTaskEnvelope {
        GeneralTaskEnvelope {
            task_id: 1,
            job_id: "job-1".to_string(),
            attempt: 1,
            spec: GeneralTaskSpec {
                task_type: "PROCESS".to_string(),
                version: "1".to_string(),
                executable: executable.to_string(),
                runtime: String::new(),
                script: String::new(),
                workload: String::new(),
                arguments: arguments.iter().map(|value| (*value).to_string()).collect(),
                environment: Vec::new(),
                working_directory: String::new(),
                stdin: Vec::new(),
                timeout_ms: 0,
                stdout_limit_bytes: 1024,
                stderr_limit_bytes: 1024,
                cpu_cores: 1,
                ram_gb: 1,
                max_ram_gb: 0,
                gpu_required: false,
                gpu_count: 0,
                vram_gb: 0,
                accelerator_type: String::new(),
                gpu_capabilities: Vec::new(),
                target_os: String::new(),
                target_arch: String::new(),
                required_runtimes: Vec::new(),
                required_capabilities: Vec::new(),
                allowed_workers: Vec::new(),
                preferred_worker: String::new(),
                input_artifacts: Vec::new(),
                output_artifacts: Vec::new(),
                workload_kind: String::new(),
                strategy: "SINGLE".to_string(),
                required_workers: 1,
                replicas: 1,
                package_manifest: None,
                max_retries: 0,
            },
        }
    }

    #[test]
    fn executes_process_and_captures_stdout() {
        #[cfg(windows)]
        let task = task("cmd.exe", &["/C", "echo hello"]);
        #[cfg(not(windows))]
        let task = task("sh", &["-c", "printf hello"]);
        let result = execute(
            task,
            Arc::new(AtomicBool::new(false)),
            std::collections::HashMap::new(),
        );
        assert_eq!(result.status, "COMPLETED");
        assert!(String::from_utf8_lossy(&result.stdout).contains("hello"));
    }

    #[test]
    fn terminates_a_timed_out_process() {
        #[cfg(windows)]
        let mut task = task("cmd.exe", &["/C", "ping -n 10 127.0.0.1 > NUL"]);
        #[cfg(not(windows))]
        let mut task = task("sh", &["-c", "sleep 10"]);
        task.spec.timeout_ms = 20;
        let result = execute(
            task,
            Arc::new(AtomicBool::new(false)),
            std::collections::HashMap::new(),
        );
        assert_eq!(result.status, "TIMED_OUT");
    }
}
