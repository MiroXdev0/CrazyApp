mod protocol;

use protocol::{
    decode_task_batch, encode_heartbeat, encode_register, encode_register_ack, encode_task_results,
    read_frame, write_frame, Frame, GPUInfo, MessageType, NodeInfo, Task, TaskResult,
};

use std::{
    env,
    io,
    net::{TcpStream, ToSocketAddrs},
    process,
    sync::{Arc, Mutex},
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

#[derive(Clone, Debug)]
struct WorkerConfig {
    controller: String,
    id: String,
    gpu_vendor: String,
    gpu_model: String,
    gpu_vram_gb: u64,
    ram_override_gb: Option<u64>,
}

fn arg_value(prefix: &str) -> Option<String> {
    env::args()
        .skip(1)
        .find_map(|arg| arg.strip_prefix(prefix).map(ToOwned::to_owned))
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
    if let Some(value) = arg_value("--ram-gb=") {
        if let Ok(value) = value.parse() {
            return value;
        }
    }

    if cfg!(target_os = "linux") {
        if let Ok(text) = std::fs::read_to_string("/proc/meminfo") {
            for line in text.lines() {
                if let Some(rest) = line.strip_prefix("MemTotal:") {
                    let kb = rest.split_whitespace().next().and_then(|v| v.parse::<u64>().ok());
                    if let Some(kb) = kb {
                        return kb / 1024 / 1024;
                    }
                }
            }
        }
    }

    0
}

fn build_config() -> WorkerConfig {
    let default_id = format!(
        "node-{}-{}",
        hostname(),
        process::id()
    );

    WorkerConfig {
        controller: arg_value("--controller=").unwrap_or_else(|| "127.0.0.1:9000".to_string()),
        id: arg_value("--id=").unwrap_or(default_id),
        gpu_vendor: arg_value("--gpu-vendor=").unwrap_or_default(),
        gpu_model: arg_value("--gpu-model=").unwrap_or_default(),
        gpu_vram_gb: arg_value("--gpu-vram-gb=")
            .and_then(|v| v.parse().ok())
            .unwrap_or(0),
        ram_override_gb: arg_value("--ram-gb=").and_then(|v| v.parse().ok()),
    }
}

fn local_node_info(config: &WorkerConfig) -> NodeInfo {
    NodeInfo {
        id: config.id.clone(),
        hostname: hostname(),
        os: os_name().to_string(),
        arch: arch_name().to_string(),
        cpu_cores: thread::available_parallelism().map(|n| n.get() as u32).unwrap_or(1),
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

fn execute_task(node: &NodeInfo, task: &Task) -> TaskResult {
    let started = Instant::now();

    if !can_run(node, task) {
        return TaskResult {
            task_id: task.id,
            job_id: task.job_id.clone(),
            status: "FAILED".to_string(),
            value: 0,
            error: "worker does not satisfy task resource requirements".to_string(),
            duration_us: started.elapsed().as_micros() as u64,
            node_id: node.id.clone(),
        };
    }

    match task.command.as_str() {
        "sum" => {
            let value = task.payload.iter().map(|v| i64::from(*v)).sum();
            TaskResult {
                task_id: task.id,
                job_id: task.job_id.clone(),
                status: "COMPLETED".to_string(),
                value,
                error: String::new(),
                duration_us: started.elapsed().as_micros() as u64,
                node_id: node.id.clone(),
            }
        }
        "xor" => {
            let value = task.payload.iter().fold(0i64, |acc, v| acc ^ i64::from(*v));
            TaskResult {
                task_id: task.id,
                job_id: task.job_id.clone(),
                status: "COMPLETED".to_string(),
                value,
                error: String::new(),
                duration_us: started.elapsed().as_micros() as u64,
                node_id: node.id.clone(),
            }
        }
        _ => TaskResult {
            task_id: task.id,
            job_id: task.job_id.clone(),
            status: "FAILED".to_string(),
            value: 0,
            error: format!("unsupported command: {}", task.command),
            duration_us: started.elapsed().as_micros() as u64,
            node_id: node.id.clone(),
        },
    }
}

fn connect(config: &WorkerConfig) -> io::Result<TcpStream> {
    let addr = config
        .controller
        .to_socket_addrs()?
        .next()
        .ok_or_else(|| io::Error::new(io::ErrorKind::AddrNotAvailable, "controller address not resolved"))?;

    let stream = TcpStream::connect_timeout(&addr, Duration::from_secs(5))?;
    stream.set_nodelay(true)?;
    Ok(stream)
}

fn run(config: WorkerConfig) -> io::Result<()> {
    let node = local_node_info(&config);
    println!(
        "[nodren-worker] id={} cpu={} ram={}GB gpu={}",
        node.id, node.cpu_cores, node.ram_gb, node.gpu.model
    );

    let stream = connect(&config)?;
    let writer = Arc::new(Mutex::new(stream.try_clone()?));

    let heartbeat_writer = Arc::clone(&writer);
    thread::spawn(move || loop {
        thread::sleep(Duration::from_secs(5));
        let payload = encode_heartbeat(now_millis());
        let mut stream = match heartbeat_writer.lock() {
            Ok(stream) => stream,
            Err(_) => return,
        };
        if write_frame(&mut *stream, MessageType::Heartbeat, now_millis() as u64, &payload).is_err() {
            return;
        }
    });

    {
        let payload = encode_register(&node)?;
        let mut stream = writer
            .lock()
            .map_err(|_| io::Error::new(io::ErrorKind::Other, "writer mutex poisoned"))?;
        write_frame(&mut *stream, MessageType::Register, 1, &payload)?;
    }

    let mut reader = stream;
    loop {
        let Frame { typ, request_id, payload } = read_frame(&mut reader)?;

        match typ {
            MessageType::RegisterAck => {
                println!("[nodren-worker] registered");
                let mut stream = writer.lock().unwrap();
                let payload = encode_register_ack("ready")?;
                write_frame(&mut *stream, MessageType::Ready, request_id, &payload)?;
            }
            MessageType::HeartbeatAck => {}
            MessageType::TaskBatch => {
                let tasks = decode_task_batch(&payload)?;
                let results: Vec<_> = tasks.iter().map(|task| execute_task(&node, task)).collect();
                let result_payload = encode_task_results(&results)?;
                let mut stream = writer.lock().unwrap();
                write_frame(&mut *stream, MessageType::TaskResultBatch, request_id, &result_payload)?;
            }
            MessageType::Error => {
                eprintln!("[nodren-worker] controller error");
            }
            MessageType::Goodbye => return Ok(()),
            MessageType::Ready | MessageType::Hello => {}
            MessageType::Register | MessageType::TaskResultBatch => {}
        }
    }
}

fn main() {
    let config = build_config();
    if let Err(err) = run(config) {
        eprintln!("[nodren-worker] fatal: {err}");
        process::exit(1);
    }
}
