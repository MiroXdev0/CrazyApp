mod executor;
mod hashing;
mod llama_cpp;
mod native_core;
mod process_executor;
mod protocol;

use executor::WorkerExecutor;
use native_core::NativeCore;
use protocol::{
    Frame, GPUInfo, MessageType, NodeInfo, Task, TaskResult, decode_artifact_begin,
    decode_artifact_chunk, decode_artifact_end, decode_general_task, decode_task_batch,
    encode_heartbeat, encode_heartbeat_with_counters_and_gpu, encode_register, encode_register_ack,
    read_frame, write_frame,
};

#[cfg(target_os = "linux")]
use std::collections::HashSet;

use std::{
    env, io,
    net::{TcpStream, ToSocketAddrs},
    process,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

#[derive(Clone, Debug)]
struct WorkerConfig {
    controller: String,
    id: String,
    cpu_override: Option<u32>,
    worker_concurrency_override: Option<u32>,
    gpu_vendor: String,
    gpu_model: String,
    gpu_vram_gb: u64,
    gpu_count: u32,
    ram_override_gb: Option<u64>,
}

fn arg_value(name: &str) -> Option<String> {
    let args: Vec<String> = env::args().skip(1).collect();
    args.iter().enumerate().find_map(|(index, arg)| {
        if let Some(value) = arg.strip_prefix(&format!("{name}=")) {
            return Some(value.to_string());
        }
        if arg == name {
            return args.get(index + 1).cloned();
        }
        None
    })
}

fn hostname() -> String {
    env::var("COMPUTERNAME")
        .or_else(|_| env::var("HOSTNAME"))
        .unwrap_or_else(|_| "unknown-host".to_string())
}

fn os_name() -> &'static str {
    if cfg!(target_os = "windows") {
        "Windows"
    } else if cfg!(target_os = "linux") {
        "Linux"
    } else if cfg!(target_os = "macos") {
        "macOS"
    } else {
        "Unknown"
    }
}

fn arch_name() -> &'static str {
    std::env::consts::ARCH
}

fn cpu_model() -> String {
    if cfg!(target_os = "linux") {
        if let Ok(text) = std::fs::read_to_string("/proc/cpuinfo") {
            if let Some(model) = text
                .lines()
                .find_map(|line| line.strip_prefix("model name:").map(str::trim))
            {
                return model.to_string();
            }
        }
    }
    String::new()
}

fn cpu_topology() -> (u32, u32) {
    let logical = thread::available_parallelism()
        .map(|n| n.get() as u32)
        .unwrap_or(1)
        .max(1);

    #[cfg(target_os = "linux")]
    {
        let mut physical_ids = HashSet::new();
        if let Ok(entries) = std::fs::read_dir("/sys/devices/system/cpu") {
            for entry in entries.flatten() {
                let name = entry.file_name().to_string_lossy().into_owned();
                if !name.strip_prefix("cpu").is_some_and(|suffix| {
                    !suffix.is_empty() && suffix.chars().all(|c| c.is_ascii_digit())
                }) {
                    continue;
                }
                let topology = entry.path().join("topology");
                let package = std::fs::read_to_string(topology.join("physical_package_id"))
                    .ok()
                    .map(|value| value.trim().to_string());
                let core = std::fs::read_to_string(topology.join("core_id"))
                    .ok()
                    .map(|value| value.trim().to_string());
                if let (Some(package), Some(core)) = (package, core) {
                    physical_ids.insert((package, core));
                }
            }
        }
        if !physical_ids.is_empty() {
            return (logical, physical_ids.len() as u32);
        }
    }

    #[cfg(windows)]
    {
        // Count PROCESSOR_CORE records without assuming a single 64-bit
        // processor group. This gives a physical-core count while the
        // standard library supplies the logical capacity above.
        #[link(name = "kernel32")]
        unsafe extern "system" {
            fn GetLogicalProcessorInformationEx(
                relationship: u32,
                buffer: *mut u8,
                returned_length: *mut u32,
            ) -> i32;
        }
        let mut length = 0u32;
        let _ = unsafe { GetLogicalProcessorInformationEx(0, std::ptr::null_mut(), &mut length) };
        if length > 0 {
            let mut buffer = vec![0u8; length as usize];
            if unsafe { GetLogicalProcessorInformationEx(0, buffer.as_mut_ptr(), &mut length) } != 0
            {
                let mut offset = 0usize;
                let mut physical = 0u32;
                while offset + 8 <= length as usize {
                    let relationship =
                        u32::from_ne_bytes(buffer[offset..offset + 4].try_into().unwrap());
                    let size =
                        u32::from_ne_bytes(buffer[offset + 4..offset + 8].try_into().unwrap())
                            as usize;
                    if size < 8 || offset + size > length as usize {
                        break;
                    }
                    if relationship == 0 {
                        physical = physical.saturating_add(1);
                    }
                    offset += size;
                }
                if physical > 0 {
                    return (logical, physical);
                }
            }
        }
    }

    // Keep logical capacity exact on platforms that do not expose physical
    // topology through a stable system interface.
    (logical, logical)
}

fn effective_worker_concurrency(available_cpu: u32, requested: Option<u32>) -> usize {
    requested
        .unwrap_or(available_cpu)
        .clamp(1, available_cpu.max(1)) as usize
}

fn available_runtimes() -> Vec<String> {
    let candidates = [
        "python",
        "python3",
        "node",
        "nodejs",
        "bash",
        "sh",
        "powershell",
        "pwsh",
    ];
    let mut runtimes = candidates
        .iter()
        .filter(|runtime| {
            process::Command::new(runtime)
                .arg("--version")
                .output()
                .is_ok()
        })
        .map(|runtime| (*runtime).to_string())
        .collect::<Vec<_>>();

    if llama_cpp::detect().is_some() && !runtimes.iter().any(|value| value == "llama.cpp") {
        runtimes.push("llama.cpp".to_string());
    }
    if llama_cpp::detect()
        .and_then(|runtime| runtime.gpu_backend().map(str::to_string))
        .is_some()
    {
        runtimes.push("llama.cpp-gpu".to_string());
    }

    // Runtime capabilities are advertised only after the worker can import
    // the actual package. This prevents the controller from scheduling an
    // ONNX job onto a machine that merely has a Python executable.
    for runtime in ["python", "python3"] {
        if !runtimes.iter().any(|value| value == runtime) {
            continue;
        }
        let probe = process::Command::new(runtime)
            .args([
                "-c",
                "import onnxruntime as o; print(','.join(o.get_available_providers()))",
            ])
            .output();
        let Ok(output) = probe else { continue };
        if !output.status.success() {
            continue;
        }
        let providers = String::from_utf8_lossy(&output.stdout);
        if providers
            .split(',')
            .any(|value| value.trim() == "CPUExecutionProvider")
        {
            if !runtimes.iter().any(|value| value == "onnxruntime") {
                runtimes.push("onnxruntime".to_string());
            }
        }
        if providers
            .split(',')
            .any(|value| value.trim() == "CUDAExecutionProvider")
        {
            if !runtimes.iter().any(|value| value == "onnxruntime-cuda") {
                runtimes.push("onnxruntime-cuda".to_string());
            }
        }
        break;
    }
    runtimes
}

fn detect_ram_gb() -> u64 {
    if cfg!(target_os = "linux") {
        if let Ok(text) = std::fs::read_to_string("/proc/meminfo") {
            for line in text.lines() {
                if let Some(rest) = line.strip_prefix("MemTotal:") {
                    let kb = rest
                        .split_whitespace()
                        .next()
                        .and_then(|v| v.parse::<u64>().ok());
                    if let Some(kb) = kb {
                        return kb / 1024 / 1024;
                    }
                }
            }
        }
    }

    #[cfg(windows)]
    {
        #[repr(C)]
        struct MemoryStatusEx {
            dw_length: u32,
            dw_memory_load: u32,
            ull_total_phys: u64,
            ull_avail_phys: u64,
            ull_total_page_file: u64,
            ull_avail_page_file: u64,
            ull_total_virtual: u64,
            ull_avail_virtual: u64,
            ull_avail_extended_virtual: u64,
        }

        unsafe extern "system" {
            fn GlobalMemoryStatusEx(status: *mut MemoryStatusEx) -> i32;
        }

        let mut status = MemoryStatusEx {
            dw_length: std::mem::size_of::<MemoryStatusEx>() as u32,
            dw_memory_load: 0,
            ull_total_phys: 0,
            ull_avail_phys: 0,
            ull_total_page_file: 0,
            ull_avail_page_file: 0,
            ull_total_virtual: 0,
            ull_avail_virtual: 0,
            ull_avail_extended_virtual: 0,
        };
        if unsafe { GlobalMemoryStatusEx(&mut status) } != 0 {
            return status.ull_total_phys / 1024 / 1024 / 1024;
        }
    }

    0
}

fn build_config() -> WorkerConfig {
    let default_id = format!("node-{}-{}", hostname(), process::id());

    WorkerConfig {
        controller: arg_value("--controller")
            .or_else(|| env::var("NODREN_CONTROLLER_ADDR").ok())
            .unwrap_or_else(|| "127.0.0.1:9000".to_string()),
        id: arg_value("--id")
            .or_else(|| env::var("NODREN_WORKER_ID").ok())
            .unwrap_or(default_id),
        cpu_override: arg_value("--cpu-cores").and_then(|v| v.parse().ok()),
        worker_concurrency_override: arg_value("--worker-concurrency")
            .or_else(|| env::var("NODREN_WORKER_CONCURRENCY").ok())
            .and_then(|v| v.parse().ok()),
        gpu_vendor: arg_value("--gpu-vendor").unwrap_or_default(),
        gpu_model: arg_value("--gpu-model").unwrap_or_default(),
        gpu_vram_gb: arg_value("--gpu-vram-gb")
            .and_then(|v| v.parse().ok())
            .unwrap_or(0),
        gpu_count: arg_value("--gpu-count")
            .and_then(|v| v.parse().ok())
            .unwrap_or(0),
        ram_override_gb: arg_value("--ram-gb").and_then(|v| v.parse().ok()),
    }
}

fn detect_nvidia_gpu() -> GPUInfo {
    let output = process::Command::new("nvidia-smi")
        .args([
            "--query-gpu=name,memory.total,driver_version,compute_cap",
            "--format=csv,noheader,nounits",
        ])
        .output();
    let Ok(output) = output else {
        return GPUInfo {
            vendor: String::new(),
            model: String::new(),
            vram_gb: 0,
            count: 0,
            capabilities: Vec::new(),
            driver: String::new(),
            runtime: String::new(),
        };
    };
    if !output.status.success() {
        return GPUInfo {
            vendor: String::new(),
            model: String::new(),
            vram_gb: 0,
            count: 0,
            capabilities: Vec::new(),
            driver: String::new(),
            runtime: String::new(),
        };
    }
    let output_text = String::from_utf8_lossy(&output.stdout);
    let rows = output_text
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .collect::<Vec<_>>();
    let Some(first) = rows.first() else {
        return GPUInfo {
            vendor: String::new(),
            model: String::new(),
            vram_gb: 0,
            count: 0,
            capabilities: Vec::new(),
            driver: String::new(),
            runtime: String::new(),
        };
    };
    let fields = first.split(',').map(str::trim).collect::<Vec<_>>();
    let total_vram_mb = rows
        .iter()
        .filter_map(|row| row.split(',').nth(1)?.trim().parse::<u64>().ok())
        .sum::<u64>();
    let mut capabilities = vec!["cuda".to_string()];
    if let Some(compute_capability) = fields.get(3).filter(|value| !value.is_empty()) {
        capabilities.push(format!("cuda_compute_{compute_capability}"));
    }
    GPUInfo {
        vendor: "NVIDIA".to_string(),
        model: fields.first().copied().unwrap_or_default().to_string(),
        vram_gb: total_vram_mb.div_ceil(1024),
        count: rows.len() as u32,
        capabilities,
        driver: fields.get(2).copied().unwrap_or_default().to_string(),
        runtime: "CUDA".to_string(),
    }
}

fn local_gpu_info(config: &WorkerConfig) -> GPUInfo {
    let detected = detect_nvidia_gpu();
    let model = if config.gpu_model.is_empty() {
        detected.model
    } else {
        config.gpu_model.clone()
    };
    let count = if config.gpu_count > 0 {
        config.gpu_count
    } else if !config.gpu_model.is_empty() {
        1
    } else {
        detected.count
    };
    GPUInfo {
        vendor: if config.gpu_vendor.is_empty() {
            detected.vendor
        } else {
            config.gpu_vendor.clone()
        },
        model,
        vram_gb: if config.gpu_vram_gb > 0 {
            config.gpu_vram_gb
        } else {
            detected.vram_gb
        },
        count,
        capabilities: detected.capabilities,
        driver: detected.driver,
        runtime: detected.runtime,
    }
}

fn nvidia_gpu_telemetry() -> (Option<u64>, Option<f64>) {
    let output = process::Command::new("nvidia-smi")
        .args([
            "--query-gpu=memory.free,utilization.gpu",
            "--format=csv,noheader,nounits",
        ])
        .output();
    let Ok(output) = output else {
        return (None, None);
    };
    if !output.status.success() {
        return (None, None);
    }

    let output_text = String::from_utf8_lossy(&output.stdout);
    let rows = output_text
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .map(str::to_string)
        .collect::<Vec<_>>();
    if rows.is_empty() {
        return (None, None);
    }
    let mut free_mb = 0u64;
    let mut utilization_sum = 0.0f64;
    let mut free_known = true;
    let mut utilization_known = true;
    for row in rows.iter() {
        let fields = row.split(',').map(str::trim).collect::<Vec<_>>();
        match fields.first().and_then(|value| value.parse::<u64>().ok()) {
            Some(value) => free_mb = free_mb.saturating_add(value),
            None => free_known = false,
        }
        match fields.get(1).and_then(|value| value.parse::<f64>().ok()) {
            Some(value) if value.is_finite() => utilization_sum += value,
            _ => utilization_known = false,
        }
    }
    let available_gb = free_known.then(|| free_mb / 1024);
    let utilization = utilization_known
        .then(|| utilization_sum / rows.len() as f64)
        .filter(|value| value.is_finite());
    (available_gb, utilization)
}

fn local_node_info(config: &WorkerConfig) -> NodeInfo {
    let (logical_cpu_cores, physical_cpu_cores) = cpu_topology();
    let advertised_cpu = config.cpu_override.unwrap_or(logical_cpu_cores).max(1);
    // CPUCores is the schedulable capacity used by the controller. The
    // topology fields retain the actual hardware capacity, so an intentional
    // concurrency limit never masquerades as a smaller physical machine.
    let cpu_cores =
        effective_worker_concurrency(advertised_cpu, config.worker_concurrency_override) as u32;
    let runtimes = available_runtimes();
    let llama_runtime = llama_cpp::detect();
    let gpu = local_gpu_info(config);
    NodeInfo {
        id: config.id.clone(),
        hostname: hostname(),
        os: os_name().to_string(),
        arch: arch_name().to_string(),
        cpu_model: cpu_model(),
        cpu_cores,
        logical_cpu_cores,
        physical_cpu_cores,
        ram_gb: config.ram_override_gb.unwrap_or_else(detect_ram_gb),
        gpu: gpu.clone(),
        runtimes: runtimes.clone(),
        execution_types: vec![
            "PROCESS".to_string(),
            "SCRIPT".to_string(),
            "COMMAND".to_string(),
            "NATIVE_WORKLOAD".to_string(),
        ],
        capabilities: {
            let mut values = vec!["process".to_string(), "native_workload".to_string()];
            if runtimes.iter().any(|value| value == "onnxruntime") {
                values.push("onnxruntime".to_string());
            }
            if runtimes.iter().any(|value| value == "onnxruntime-cuda") {
                values.push("onnxruntime-cuda".to_string());
            }
            if runtimes.iter().any(|value| value == "llama.cpp") {
                values.push("llama.cpp".to_string());
                values.push("llama.cpp-cpu".to_string());
                if let Some(runtime) = &llama_runtime {
                    values.push(format!("llama.cpp-version:{}", runtime.version()));
                    if let Some(backend) = runtime.gpu_backend() {
                        values.push(format!("llama.cpp-backend:{backend}"));
                    }
                }
            }
            if runtimes.iter().any(|value| value == "llama.cpp-gpu")
                && gpu.count > 0
                && !gpu.runtime.is_empty()
            {
                values.push("llama.cpp-gpu".to_string());
            }
            values
        },
    }
}

fn can_run(node: &NodeInfo, task: &Task) -> bool {
    node.cpu_cores >= task.requirements.cpu_cores
        && node.ram_gb >= task.requirements.ram_gb
        && (!task.requirements.gpu_required || !node.gpu.model.is_empty())
}

fn now_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64
}

fn decode_task_cancel(data: &[u8]) -> io::Result<u64> {
    if data.len() != 8 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid task cancellation payload",
        ));
    }
    Ok(u64::from_le_bytes(data.try_into().unwrap()))
}

fn cpu_sample() -> Option<(u64, u64)> {
    if cfg!(target_os = "linux") {
        let line = std::fs::read_to_string("/proc/stat")
            .ok()?
            .lines()
            .next()?
            .to_string();
        let values: Vec<u64> = line
            .split_whitespace()
            .skip(1)
            .filter_map(|value| value.parse().ok())
            .collect();
        if values.len() < 4 {
            return None;
        }
        let idle = values[3].saturating_add(*values.get(4).unwrap_or(&0));
        let total: u64 = values.iter().copied().sum();
        return Some((total.saturating_sub(idle), total));
    }

    #[cfg(windows)]
    {
        #[repr(C)]
        struct FileTime {
            low: u32,
            high: u32,
        }

        unsafe extern "system" {
            fn GetSystemTimes(
                idle: *mut FileTime,
                kernel: *mut FileTime,
                user: *mut FileTime,
            ) -> i32;
        }

        let mut idle = FileTime { low: 0, high: 0 };
        let mut kernel = FileTime { low: 0, high: 0 };
        let mut user = FileTime { low: 0, high: 0 };
        if unsafe { GetSystemTimes(&mut idle, &mut kernel, &mut user) } != 0 {
            let to_u64 = |value: &FileTime| ((value.high as u64) << 32) | value.low as u64;
            let idle_ticks = to_u64(&idle);
            let total_ticks = to_u64(&kernel).saturating_add(to_u64(&user));
            return Some((total_ticks.saturating_sub(idle_ticks), total_ticks));
        }
    }

    None
}

fn available_memory_gb() -> Option<u64> {
    if cfg!(target_os = "linux") {
        let text = std::fs::read_to_string("/proc/meminfo").ok()?;
        return text.lines().find_map(|line| {
            let rest = line.strip_prefix("MemAvailable:")?;
            let kb = rest.split_whitespace().next()?.parse::<u64>().ok()?;
            Some(kb / 1024 / 1024)
        });
    }

    #[cfg(windows)]
    {
        #[repr(C)]
        struct MemoryStatusEx {
            dw_length: u32,
            dw_memory_load: u32,
            ull_total_phys: u64,
            ull_avail_phys: u64,
            ull_total_page_file: u64,
            ull_avail_page_file: u64,
            ull_total_virtual: u64,
            ull_avail_virtual: u64,
            ull_avail_extended_virtual: u64,
        }

        unsafe extern "system" {
            fn GlobalMemoryStatusEx(status: *mut MemoryStatusEx) -> i32;
        }

        let mut status = MemoryStatusEx {
            dw_length: std::mem::size_of::<MemoryStatusEx>() as u32,
            dw_memory_load: 0,
            ull_total_phys: 0,
            ull_avail_phys: 0,
            ull_total_page_file: 0,
            ull_avail_page_file: 0,
            ull_total_virtual: 0,
            ull_avail_virtual: 0,
            ull_avail_extended_virtual: 0,
        };
        if unsafe { GlobalMemoryStatusEx(&mut status) } != 0 {
            return Some(status.ull_avail_phys / 1024 / 1024 / 1024);
        }
    }

    None
}

fn resource_failure_result(node: &NodeInfo, task: &Task) -> TaskResult {
    TaskResult {
        task_id: task.id,
        job_id: task.job_id.clone(),
        status: "FAILED".to_string(),
        value: 0,
        error_code: "resource_requirements".to_string(),
        error: "worker does not satisfy task resource requirements".to_string(),
        duration_us: 0,
        node_id: node.id.clone(),
    }
}

fn execute_task(node: &NodeInfo, core: &NativeCore, task: &Task) -> TaskResult {
    let started = Instant::now();

    if !can_run(node, task) {
        let mut result = resource_failure_result(node, task);
        result.duration_us = started.elapsed().as_micros() as u64;
        return result;
    }

    match core.execute(task.id, &task.command, &task.payload) {
        Ok(value) => TaskResult {
            task_id: task.id,
            job_id: task.job_id.clone(),
            status: "COMPLETED".to_string(),
            value,
            error_code: String::new(),
            error: String::new(),
            duration_us: started.elapsed().as_micros() as u64,
            node_id: node.id.clone(),
        },
        Err(error) => TaskResult {
            task_id: task.id,
            job_id: task.job_id.clone(),
            status: "FAILED".to_string(),
            value: 0,
            error_code: native_error_category(error.code).to_string(),
            error: error.message,
            duration_us: started.elapsed().as_micros() as u64,
            node_id: node.id.clone(),
        },
    }
}

fn native_error_category(code: i32) -> &'static str {
    match code {
        1 => "invalid_argument",
        2 => "malformed_payload",
        3 => "unsupported_workload",
        4 => "execution_failed",
        _ => "internal_error",
    }
}

fn connect(config: &WorkerConfig) -> io::Result<TcpStream> {
    let addr = config.controller.to_socket_addrs()?.next().ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::AddrNotAvailable,
            "controller address not resolved",
        )
    })?;

    let stream = TcpStream::connect_timeout(&addr, Duration::from_secs(5))?;
    stream.set_nodelay(true)?;
    Ok(stream)
}

fn run_connection(
    config: &WorkerConfig,
    node: &NodeInfo,
    executor: &WorkerExecutor,
    reconnected: bool,
    started_at: Instant,
) -> io::Result<()> {
    println!(
        "[nodren-worker] connecting controller={}",
        config.controller
    );
    let stream = connect(config)?;
    stream.set_read_timeout(Some(Duration::from_secs(1)))?;
    println!("[nodren-worker] connected");
    if reconnected {
        println!("[nodren-worker] reconnected");
    }

    let writer = Arc::new(Mutex::new(stream.try_clone()?));
    let stop_heartbeat = Arc::new(AtomicBool::new(false));
    let connection_failed = Arc::new(AtomicBool::new(false));
    let heartbeat_writer = Arc::clone(&writer);
    let heartbeat_stop = Arc::clone(&stop_heartbeat);
    let heartbeat_error = Arc::clone(&connection_failed);
    let active_tasks = executor.active_tasks_counter();
    let completed_tasks = executor.completed_tasks_counter();
    let failed_tasks = executor.failed_tasks_counter();
    let heartbeat_thread = thread::spawn(move || {
        let mut previous_cpu = None;
        loop {
            for _ in 0..50 {
                if heartbeat_stop.load(Ordering::Acquire) {
                    return;
                }
                thread::sleep(Duration::from_millis(100));
            }

            let cpu_percent = cpu_sample().and_then(|current| {
                let previous = previous_cpu.replace(current)?;
                let busy_delta = current.0.saturating_sub(previous.0);
                let total_delta = current.1.saturating_sub(previous.1);
                if total_delta == 0 {
                    None
                } else {
                    Some((busy_delta as f64 / total_delta as f64) * 100.0)
                }
            });
            let (gpu_available_vram_gb, gpu_utilization_percent) = nvidia_gpu_telemetry();
            let payload = encode_heartbeat_with_counters_and_gpu(
                now_millis(),
                started_at.elapsed().as_secs(),
                active_tasks.load(Ordering::Acquire),
                completed_tasks.load(Ordering::Acquire),
                failed_tasks.load(Ordering::Acquire),
                cpu_percent,
                available_memory_gb(),
                gpu_available_vram_gb,
                gpu_utilization_percent,
            );
            let mut stream = match heartbeat_writer.lock() {
                Ok(stream) => stream,
                Err(_) => return,
            };
            if write_frame(
                &mut *stream,
                MessageType::Heartbeat,
                now_millis() as u64,
                &payload,
            )
            .is_err()
            {
                heartbeat_error.store(true, Ordering::Release);
                return;
            }
        }
    });

    let connection_result = (|| {
        let payload = encode_register(node)?;
        let mut writer_stream = writer
            .lock()
            .map_err(|_| io::Error::other("writer mutex poisoned"))?;
        write_frame(&mut *writer_stream, MessageType::Register, 1, &payload)?;
        drop(writer_stream);

        let mut reader = stream;
        loop {
            let frame = match read_frame(&mut reader) {
                Ok(frame) => frame,
                Err(error)
                    if matches!(
                        error.kind(),
                        io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
                    ) =>
                {
                    if connection_failed.load(Ordering::Acquire) {
                        break Err(io::Error::new(
                            io::ErrorKind::ConnectionAborted,
                            "heartbeat write failed",
                        ));
                    }
                    continue;
                }
                Err(error) => break Err(error),
            };

            let Frame {
                typ,
                request_id,
                payload,
            } = frame;

            match typ {
                MessageType::RegisterAck => {
                    println!("[nodren-worker] registered");
                    let mut stream = writer.lock().unwrap();
                    let payload = encode_register_ack("ready")?;
                    write_frame(&mut *stream, MessageType::Ready, request_id, &payload)?;
                    println!("[nodren-worker] ready");
                }
                MessageType::HeartbeatAck => {}
                MessageType::Heartbeat => {
                    let mut stream = writer.lock().unwrap();
                    write_frame(
                        &mut *stream,
                        MessageType::HeartbeatAck,
                        request_id,
                        &encode_heartbeat(now_millis(), 0, 0, None, None),
                    )?;
                }
                MessageType::TaskBatch => {
                    let tasks = decode_task_batch(&payload)?;
                    for task in tasks {
                        executor.submit(
                            task,
                            request_id,
                            Arc::clone(&writer),
                            Arc::clone(&connection_failed),
                        )?;
                    }
                }
                MessageType::TaskSubmit => {
                    let task = decode_general_task(&payload)?;
                    executor.submit_general(
                        task,
                        request_id,
                        node.clone(),
                        Arc::clone(&writer),
                        Arc::clone(&connection_failed),
                    )?;
                }
                MessageType::TaskCancel => {
                    let task_id = decode_task_cancel(&payload)?;
                    if !executor.cancel_general(task_id) {
                        eprintln!("[nodren-worker] task cancellation ignored task_id={task_id}");
                    }
                }
                MessageType::ArtifactBegin => {
                    if let Err(error) = executor.begin_artifact(decode_artifact_begin(&payload)?) {
                        eprintln!("[nodren-worker] artifact begin failed: {error}");
                    }
                }
                MessageType::ArtifactChunk => {
                    if let Err(error) = executor.append_artifact(decode_artifact_chunk(&payload)?) {
                        eprintln!("[nodren-worker] artifact chunk failed: {error}");
                    }
                }
                MessageType::ArtifactEnd => {
                    let (task_id, artifact_id) = decode_artifact_end(&payload)?;
                    if let Err(error) = executor.finish_artifact(task_id, artifact_id) {
                        eprintln!("[nodren-worker] artifact finalize failed: {error}");
                    }
                }
                MessageType::Error => {
                    eprintln!("[nodren-worker] controller error");
                }
                MessageType::Goodbye => break Ok(()),
                MessageType::Ready | MessageType::Hello => {}
                MessageType::Register
                | MessageType::TaskResultBatch
                | MessageType::TaskAck
                | MessageType::TaskState
                | MessageType::TaskResult
                | MessageType::Capabilities => {}
            }
        }
    })();

    stop_heartbeat.store(true, Ordering::Release);
    let _ = heartbeat_thread.join();
    connection_result
}

fn run(config: WorkerConfig) -> io::Result<()> {
    let node = local_node_info(&config);
    let worker_concurrency =
        effective_worker_concurrency(node.cpu_cores, config.worker_concurrency_override);
    let core = Arc::new(NativeCore::load_with_worker_count(worker_concurrency)?);
    let executor = WorkerExecutor::new(node.clone(), Arc::clone(&core), worker_concurrency);
    let started_at = Instant::now();
    println!(
        "[nodren-worker] id={} cpu={} logical={} physical={} concurrency={} ram={}GB gpu={}",
        node.id,
        node.cpu_cores,
        node.logical_cpu_cores,
        node.physical_cpu_cores,
        worker_concurrency,
        node.ram_gb,
        node.gpu.model
    );

    let mut reconnected = false;
    let mut backoff = Duration::from_millis(250);
    loop {
        match run_connection(&config, &node, &executor, reconnected, started_at) {
            Ok(()) => return Ok(()),
            Err(error) => {
                eprintln!("[nodren-worker] connection lost: {error}");
                eprintln!("[nodren-worker] reconnecting in {}ms", backoff.as_millis());
                thread::sleep(backoff);
                backoff = (backoff * 2).min(Duration::from_secs(5));
                reconnected = true;
            }
        }
    }
}

fn main() {
    if env::args().any(|arg| arg == "--help" || arg == "-h") {
        print_help();
        return;
    }
    if env::args().any(|arg| arg == "--version" || arg == "-V") {
        println!("Nodren Worker 0.2.0");
        return;
    }
    let config = build_config();
    if let Err(err) = run(config) {
        eprintln!("[nodren-worker] fatal: {err}");
        process::exit(1);
    }
}

fn print_help() {
    println!(
        "Nodren Worker 0.2.0\n\nUsage:\n    nodren-worker.exe --controller <host:port> [options]\n\nOptions:\n    --controller <host:port>    Controller TCP address\n    --id <worker-id>            Stable worker identity\n    --cpu-cores <count>         Override discovered logical CPU capacity\n    --worker-concurrency <n>    Limit executor/Core concurrency (default: all logical CPUs)\n    --ram-gb <count>            Override discovered RAM capacity\n    --gpu-vendor <name>         Advertised GPU vendor\n    --gpu-model <name>          Advertised GPU model\n    --gpu-vram-gb <count>       Advertised GPU memory\n    --gpu-count <count>         Advertised GPU count\n\nNVIDIA metadata is discovered with nvidia-smi when available; explicit GPU\noptions override discovered values.\n\nEnvironment:\n    NODREN_CONTROLLER_ADDR       Fallback Controller TCP address\n    NODREN_WORKER_ID             Fallback worker identity\n    NODREN_WORKER_CONCURRENCY    Fallback executor/Core concurrency override\n"
    );
}

#[cfg(test)]
mod tests {
    use super::effective_worker_concurrency;

    #[test]
    fn concurrency_defaults_to_full_available_capacity() {
        assert_eq!(effective_worker_concurrency(4, None), 4);
        assert_eq!(effective_worker_concurrency(64, None), 64);
    }

    #[test]
    fn concurrency_override_is_explicit_and_bounded() {
        assert_eq!(effective_worker_concurrency(64, Some(12)), 12);
        assert_eq!(effective_worker_concurrency(64, Some(128)), 64);
        assert_eq!(effective_worker_concurrency(4, Some(0)), 1);
    }
}
