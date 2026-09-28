mod executor;
mod native_core;
mod protocol;

use executor::WorkerExecutor;
use native_core::NativeCore;
use protocol::{
    Frame, GPUInfo, MessageType, NodeInfo, Task, TaskResult, decode_task_batch, encode_heartbeat,
    encode_register, encode_register_ack, read_frame, write_frame,
};

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
    gpu_vendor: String,
    gpu_model: String,
    gpu_vram_gb: u64,
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
        gpu_vendor: arg_value("--gpu-vendor").unwrap_or_default(),
        gpu_model: arg_value("--gpu-model").unwrap_or_default(),
        gpu_vram_gb: arg_value("--gpu-vram-gb")
            .and_then(|v| v.parse().ok())
            .unwrap_or(0),
        ram_override_gb: arg_value("--ram-gb").and_then(|v| v.parse().ok()),
    }
}

fn local_node_info(config: &WorkerConfig) -> NodeInfo {
    NodeInfo {
        id: config.id.clone(),
        hostname: hostname(),
        os: os_name().to_string(),
        arch: arch_name().to_string(),
        cpu_cores: config.cpu_override.unwrap_or_else(|| {
            thread::available_parallelism()
                .map(|n| n.get() as u32)
                .unwrap_or(1)
        }),
        ram_gb: config.ram_override_gb.unwrap_or_else(detect_ram_gb),
        gpu: GPUInfo {
            vendor: config.gpu_vendor.clone(),
            model: config.gpu_model.clone(),
            vram_gb: config.gpu_vram_gb,
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
    let heartbeat_thread = thread::spawn(move || {
        loop {
            for _ in 0..50 {
                if heartbeat_stop.load(Ordering::Acquire) {
                    return;
                }
                thread::sleep(Duration::from_millis(100));
            }

            let payload = encode_heartbeat(now_millis());
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
                        &encode_heartbeat(now_millis()),
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
                MessageType::Error => {
                    eprintln!("[nodren-worker] controller error");
                }
                MessageType::Goodbye => break Ok(()),
                MessageType::Ready | MessageType::Hello => {}
                MessageType::Register | MessageType::TaskResultBatch => {}
            }
        }
    })();

    stop_heartbeat.store(true, Ordering::Release);
    let _ = heartbeat_thread.join();
    connection_result
}

fn run(config: WorkerConfig) -> io::Result<()> {
    let node = local_node_info(&config);
    let core = Arc::new(NativeCore::load()?);
    let executor = WorkerExecutor::new(node.clone(), Arc::clone(&core));
    println!(
        "[nodren-worker] id={} cpu={} ram={}GB gpu={}",
        node.id, node.cpu_cores, node.ram_gb, node.gpu.model
    );

    let mut reconnected = false;
    let mut backoff = Duration::from_millis(250);
    loop {
        match run_connection(&config, &node, &executor, reconnected) {
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
        "Nodren Worker 0.2.0\n\nUsage:\n    nodren-worker.exe --controller <host:port> [options]\n\nOptions:\n    --controller <host:port>    Controller TCP address\n    --id <worker-id>            Stable worker identity\n    --cpu-cores <count>         Override discovered CPU capacity\n    --ram-gb <count>            Override discovered RAM capacity\n    --gpu-vendor <name>         Advertised GPU vendor\n    --gpu-model <name>          Advertised GPU model\n    --gpu-vram-gb <count>       Advertised GPU memory\n\nEnvironment:\n    NODREN_CONTROLLER_ADDR       Fallback Controller TCP address\n    NODREN_WORKER_ID             Fallback worker identity\n"
    );
}
