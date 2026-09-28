use crate::api::{ApiError, Client, JobRequest, ResourceRequirements};
use crate::cli::Command;
use crate::system::platform;

use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64};
use std::{fmt, time::Duration};

const VERSION: &str = "0.2.0";
const NAME: &str = "Nodren";

#[derive(Debug)]
pub enum CommandError {
    Api(ApiError),
    Usage(String),
}

impl fmt::Display for CommandError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Api(error) => error.fmt(formatter),
            Self::Usage(message) => write!(formatter, "{message}"),
        }
    }
}

impl From<ApiError> for CommandError {
    fn from(error: ApiError) -> Self {
        Self::Api(error)
    }
}

pub fn execute(command: Command) -> Result<(), CommandError> {
    match command {
        Command::Start => start(),
        Command::Stop => stop(),
        Command::Restart => restart(),
        Command::Status => status(),
        Command::Check(device) => check(device),
        Command::Devices => devices(),
        Command::Run(args) => run(args),
        Command::Jobs => jobs(),
        Command::Logs(args) => logs(args),
        Command::Ping(device) => ping(device),
        Command::Info(device) => info(device),
        Command::Doctor => doctor(),
        Command::Config => config(),
        Command::Version => {
            version();
            Ok(())
        }
        Command::Help => {
            help();
            Ok(())
        }
    }
}

fn client() -> Client {
    Client::from_environment()
}

// ---------------------------------------------------------
// Service lifecycle
// ---------------------------------------------------------

fn start() -> Result<(), CommandError> {
    header("Starting Nodren");
    println!("[INFO] Platform: {}", platform::name());
    println!("[INFO] The CLI does not start or supervise Controller/Worker processes yet.");
    client().health()?;
    println!("[OK] Controller is already reachable; no local processes were started.");
    Ok(())
}

fn stop() -> Result<(), CommandError> {
    header("Stopping Nodren");
    println!("[INFO] Cluster lifecycle management is not implemented by this CLI yet.");
    println!("[INFO] No Controller or Worker processes were stopped.");
    Ok(())
}

fn restart() -> Result<(), CommandError> {
    header("Restarting Nodren");
    println!("[INFO] Cluster lifecycle management is not implemented by this CLI yet.");
    println!("[INFO] No Controller or Worker processes were restarted.");
    Ok(())
}

// ---------------------------------------------------------
// Status / diagnostics
// ---------------------------------------------------------

fn status() -> Result<(), CommandError> {
    let api = client();
    let health = api.health()?;
    let nodes = api.nodes()?;
    let jobs = api.jobs()?;

    let ready = nodes.iter().filter(|node| node.state == "READY").count();
    let running = jobs.iter().filter(|job| job.status == "RUNNING").count();
    let queued = jobs.iter().filter(|job| job.status == "QUEUED").count();
    let completed = jobs.iter().filter(|job| job.status == "COMPLETED").count();
    let failed = jobs.iter().filter(|job| job.status == "FAILED").count();

    header("Nodren Status");
    println!("Controller     ONLINE ({})", api.base_url());
    println!("Workers        {}", health.nodes);
    println!("Ready          {}", ready);
    println!("Running Jobs   {}", running);
    println!("Queued Jobs    {}", queued);
    println!("Completed      {}", completed);
    println!("Failed         {}", failed);
    Ok(())
}

fn check(device: Option<String>) -> Result<(), CommandError> {
    match device {
        Some(device) => check_device(&device),
        None => check_controller(),
    }
}

fn check_controller() -> Result<(), CommandError> {
    let api = client();
    header("Nodren System Check");
    println!("[OK] CLI");
    println!("[OK] Platform: {}", platform::name());
    let health = api.health()?;
    let nodes = api.nodes()?;
    println!("[OK] Controller: {} {}", health.service, health.version);
    println!("[OK] API workers: {}", nodes.len());
    println!();
    println!("[OK] Controller check complete.");
    Ok(())
}

fn check_device(device: &str) -> Result<(), CommandError> {
    let node = client().node(device)?;
    header(&format!("Checking Worker: {device}"));
    println!("[OK] Worker is registered");
    println!("[INFO] State: {}", node.state);
    println!("[INFO] Last heartbeat: {}", node.last_heartbeat);
    Ok(())
}

fn doctor() -> Result<(), CommandError> {
    let api = client();
    header("Nodren Doctor");
    println!("[OK] CLI");
    println!("[OK] Platform: {}", platform::name());
    let health = api.health()?;
    println!("[OK] Controller: {}", health.status);
    let nodes = api.nodes()?;
    let jobs = api.jobs()?;
    println!("[OK] Registered workers: {}", nodes.len());
    println!("[OK] Observed jobs: {}", jobs.len());
    println!("[INFO] Native capability details are not exposed by the Controller API.");
    Ok(())
}

fn config() -> Result<(), CommandError> {
    let api = client();
    header("Nodren Configuration");
    println!("Controller: {}", api.base_url());
    println!("Environment: NODREN_CONTROLLER_URL or NODREN_HTTP_ADDR");
    Ok(())
}

fn info(device: Option<String>) -> Result<(), CommandError> {
    match device {
        Some(device) => {
            let node = client().node(&device)?;
            header(&format!("Worker Information: {device}"));
            print_node(&node);
        }
        None => {
            header("Nodren Information");
            println!("Name       : {NAME}");
            println!("Version    : {VERSION}");
            println!("Platform   : {}", platform::name());
            println!("Controller : {}", client().base_url());
        }
    }
    Ok(())
}

// ---------------------------------------------------------
// Workers
// ---------------------------------------------------------

fn devices() -> Result<(), CommandError> {
    let nodes = client().nodes()?;
    header("Nodren Workers");
    if nodes.is_empty() {
        println!("No workers registered.");
        return Ok(());
    }
    for node in nodes {
        println!(
            "{}  state={}  CPU={}/{}  RAM={}/{}GB  jobs={}",
            node.info.id,
            node.state,
            node.allocated_cpu_cores,
            node.info.cpu_cores,
            node.allocated_ram_gb,
            node.info.ram_gb,
            node.assigned_jobs.len(),
        );
        println!(
            "  OS={} arch={} GPU={}",
            node.info.os,
            node.info.arch,
            gpu_description(&node.info.gpu),
        );
    }
    Ok(())
}

fn ping(device: String) -> Result<(), CommandError> {
    let api = client();
    let (nodes, latency) = api.nodes_with_latency()?;
    let node = nodes
        .into_iter()
        .find(|candidate| candidate.info.id == device)
        .ok_or_else(|| ApiError::UnknownNode(device.clone()))?;
    let reachable = matches!(node.state.as_str(), "READY" | "BUSY");

    header(&format!("Pinging {device}"));
    println!("Target       {}", node.info.id);
    println!(
        "Reachability {}",
        if reachable { "REACHABLE" } else { "NOT READY" }
    );
    println!("State        {}", node.state);
    println!("API latency  {} ms", latency.as_secs_f64() * 1000.0);
    println!("[INFO] Latency measures the Controller API request, not a direct worker probe.");
    Ok(())
}

fn print_node(node: &crate::api::NodeRecord) {
    println!("ID           {}", node.info.id);
    println!("State        {}", node.state);
    println!("Hostname     {}", node.info.hostname);
    println!("OS           {}", node.info.os);
    println!("Architecture {}", node.info.arch);
    println!(
        "CPU          {} / {} allocated",
        node.allocated_cpu_cores, node.info.cpu_cores
    );
    println!(
        "RAM          {} / {} GB allocated",
        node.allocated_ram_gb, node.info.ram_gb
    );
    println!(
        "Assigned     {}",
        if node.assigned_jobs.is_empty() {
            "none".to_string()
        } else {
            node.assigned_jobs.join(", ")
        }
    );
    println!("GPU          {}", gpu_description(&node.info.gpu));
    println!("Heartbeat    {}", node.last_heartbeat);
}

fn gpu_description(gpu: &crate::api::GpuInfo) -> String {
    if gpu.model.is_empty() {
        "none".to_string()
    } else {
        format!("{} {} ({}GB)", gpu.vendor, gpu.model, gpu.vram_gb)
    }
}

// ---------------------------------------------------------
// Workloads and jobs
// ---------------------------------------------------------

fn run(args: Vec<String>) -> Result<(), CommandError> {
    if args.is_empty() {
        return Err(CommandError::Usage(
            "Usage: nodren run <sum|xor|dot_product> [arguments...]".to_string(),
        ));
    }

    let command = args[0].to_ascii_lowercase();
    let payload = workload_payload(&command, &args[1..])?;
    let request = JobRequest {
        command: command.clone(),
        priority: 50,
        requirements: ResourceRequirements {
            cpu_cores: 1,
            ram_gb: 1,
            gpu_required: false,
        },
        payload_base64: Some(BASE64.encode(payload)),
    };
    let api = client();
    let submitted = api.submit_job(&request)?;
    header("Submitting Workload");
    println!("Workload     {}", submitted.command);
    println!("Job ID       {}", submitted.id);
    println!("State        {}", submitted.status);
    if !submitted.node_id.is_empty() {
        println!("Worker       {}", submitted.node_id);
    }
    println!("Waiting for result...");

    let timeout = std::env::var("NODREN_JOB_TIMEOUT_SECS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(30);
    let completed = api.wait_for_job(&submitted.id, Duration::from_secs(timeout))?;
    let result = completed
        .result
        .as_ref()
        .ok_or_else(|| ApiError::InvalidResponse("completed job has no result".to_string()))?;
    println!("State        {}", completed.status);
    println!("Worker       {}", completed.node_id);
    println!("Result       {}", result.value);
    println!("Duration     {} us", result.duration_us);
    Ok(())
}

fn jobs() -> Result<(), CommandError> {
    let jobs = client().jobs()?;
    header("Nodren Jobs");
    if jobs.is_empty() {
        println!("No jobs found.");
        return Ok(());
    }
    for job in jobs {
        let requirements = format!(
            "cpu={} ram={}GB gpu={}",
            job.requirements.cpu_cores, job.requirements.ram_gb, job.requirements.gpu_required
        );
        println!(
            "{}  {}  state={}  worker={}  {}",
            job.id,
            job.command,
            job.status,
            if job.node_id.is_empty() {
                "-"
            } else {
                &job.node_id
            },
            requirements,
        );
        if let Some(result) = job.result {
            if !result.error.is_empty() {
                println!("  error={} {}", result.error_code, result.error);
            }
        }
    }
    Ok(())
}

fn logs(args: Vec<String>) -> Result<(), CommandError> {
    header("Nodren Logs");
    if args.is_empty() {
        println!("[INFO] Centralized log retrieval is not implemented yet.");
    } else {
        println!(
            "[INFO] Log retrieval is not implemented for target(s): {}",
            args.join(", ")
        );
    }
    Ok(())
}

fn workload_payload(command: &str, args: &[String]) -> Result<Vec<u8>, CommandError> {
    match command {
        "sum" | "xor" => {
            if args.is_empty() {
                return Err(CommandError::Usage(format!(
                    "Usage: nodren run {command} <byte>..."
                )));
            }
            args.iter()
                .map(|value| {
                    value.parse::<u8>().map_err(|_| {
                        CommandError::Usage(format!(
                            "{command} arguments must be unsigned bytes: {value}"
                        ))
                    })
                })
                .collect()
        }
        "dot_product" => encode_dot_product(args),
        _ => Ok(if args.is_empty() {
            b"payload".to_vec()
        } else {
            args.join(" ").into_bytes()
        }),
    }
}

fn encode_dot_product(args: &[String]) -> Result<Vec<u8>, CommandError> {
    if args.len() != 2 {
        return Err(CommandError::Usage(
            "Usage: nodren run dot_product <left comma-separated vector> <right comma-separated vector>".to_string(),
        ));
    }
    let left = parse_vector(&args[0])?;
    let right = parse_vector(&args[1])?;
    if left.is_empty() || left.len() != right.len() {
        return Err(CommandError::Usage(
            "dot_product vectors must be non-empty and have equal length".to_string(),
        ));
    }
    let count = u32::try_from(left.len())
        .map_err(|_| CommandError::Usage("dot_product vector is too large".to_string()))?;
    let mut payload = Vec::with_capacity(4 + left.len() * 8);
    payload.extend_from_slice(&count.to_le_bytes());
    for value in left.iter().chain(right.iter()) {
        payload.extend_from_slice(&value.to_le_bytes());
    }
    Ok(payload)
}

fn parse_vector(value: &str) -> Result<Vec<i32>, CommandError> {
    value
        .split(',')
        .map(|part| {
            part.parse::<i32>().map_err(|_| {
                CommandError::Usage(format!(
                    "dot_product vector contains invalid integer: {part}"
                ))
            })
        })
        .collect()
}

// ---------------------------------------------------------
// Utility and help
// ---------------------------------------------------------

fn header(title: &str) {
    println!();
    println!("{title}");
    println!("{}", "-".repeat(title.len()));
}

fn version() {
    println!("{NAME} CLI {VERSION}");
}

fn help() {
    println!(
        r#"Nodren CLI 0.2.0

Usage:
    nodren <command> [arguments]

CONTROLLER
    status                  Show live Controller and job state
    check                   Check Controller/API availability
    doctor                  Run live diagnostics
    config                  Show Controller configuration

WORKERS
    devices                 List registered workers
    info <worker>           Show worker details
    ping <worker>           Show Controller-observed worker reachability

WORKLOADS
    run sum <byte>...       Submit and wait for a sum result
    run xor <byte>...       Submit and wait for an xor result
    run dot_product <left> <right>
                            Vectors are comma-separated integers
    jobs                    List live jobs

LIMITED
    start                   Check Controller; does not start processes
    stop                    Reports lifecycle limitation
    restart                 Reports lifecycle limitation
    logs [target]           Reports log retrieval limitation

Environment:
    NODREN_CONTROLLER_URL   Controller URL, default http://127.0.0.1:8080
    NODREN_HTTP_ADDR        Fallback host:port configuration
    NODREN_JOB_TIMEOUT_SECS Synchronous run timeout, default 30
"#
    );
}

#[cfg(test)]
mod tests {
    use super::{encode_dot_product, parse_vector, workload_payload};

    #[test]
    fn builds_sum_and_xor_byte_payloads() {
        assert_eq!(
            workload_payload("sum", &["1".into(), "2".into(), "255".into()]).unwrap(),
            vec![1, 2, 255]
        );
        assert_eq!(
            workload_payload("xor", &["1".into(), "2".into()]).unwrap(),
            vec![1, 2]
        );
    }

    #[test]
    fn builds_native_dot_product_payload() {
        let payload = encode_dot_product(&["1,2,3".into(), "4,5,6".into()]).unwrap();
        assert_eq!(
            payload,
            vec![
                3, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, 4, 0, 0, 0, 5, 0, 0, 0, 6, 0, 0, 0
            ]
        );
    }

    #[test]
    fn rejects_malformed_dot_product_input() {
        assert!(encode_dot_product(&["1,2".into(), "3".into()]).is_err());
        assert!(parse_vector("1,nope").is_err());
        assert!(workload_payload("sum", &[]).is_err());
    }

    #[test]
    fn unsupported_workloads_keep_a_nonempty_payload() {
        assert_eq!(workload_payload("future", &[]).unwrap(), b"payload");
        assert_eq!(workload_payload("future", &["raw".into()]).unwrap(), b"raw");
    }
}
