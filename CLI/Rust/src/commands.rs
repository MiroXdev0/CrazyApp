use crate::api::{
    ApiError, Client, DistributionRequest, GeneralTaskSpec, JobRequest, ResourceRequirements,
    RetryPolicy, TaskRequest,
};
use crate::cli::{Command, DistributionCommand, JobCommand, TaskCommand, WorkerCommand};
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
        Command::Workers(command) => workers(command),
        Command::Run(args) => run(args),
        Command::Jobs => jobs(),
        Command::JobControl(command) => job_control(command),
        Command::Tasks(command) => task_control(command),
        Command::Distribution(command) => distribution(command),
        Command::Events => {
            client().events()?;
            Ok(())
        }
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

fn workers(command: WorkerCommand) -> Result<(), CommandError> {
    match command {
        WorkerCommand::List => devices(),
        WorkerCommand::Info(id) => info(Some(id)),
        WorkerCommand::Ping(id) => ping(id),
        WorkerCommand::Pause(id) => worker_action(&id, "pause"),
        WorkerCommand::Resume(id) => worker_action(&id, "resume"),
        WorkerCommand::Remove(id) => worker_action(&id, "remove"),
        WorkerCommand::Stats(Some(id)) => print_worker_stats(&client().worker_stats(&id)?),
        WorkerCommand::Stats(None) => {
            for node in client().nodes()? {
                print_worker_stats(&client().worker_stats(&node.info.id)?);
            }
            Ok(())
        }
    }
}

fn worker_action(id: &str, action: &str) -> Result<(), CommandError> {
    let node = client().worker_action(id, action)?;
    println!("Worker {}: {}", node.info.id, node.state);
    Ok(())
}

fn job_control(command: JobCommand) -> Result<(), CommandError> {
    match command {
        JobCommand::List => jobs(),
        JobCommand::Run(args) => run(args),
        JobCommand::Info(id) => {
            let job = client().job(&id)?;
            print_job(&job);
            Ok(())
        }
        JobCommand::Cancel(id) => job_action(&id, "cancel"),
        JobCommand::Pause(id) => job_action(&id, "pause"),
        JobCommand::Resume(id) => job_action(&id, "resume"),
        JobCommand::Stats(id) => {
            let stats = client().job_stats(&id)?;
            print_job(&stats.job);
            println!("Queue time   {} ms", stats.queue_time_ms);
            println!("Elapsed      {} ms", stats.elapsed_ms);
            println!(
                "Active nodes {}",
                if stats.active_workers.is_empty() {
                    "-".to_string()
                } else {
                    stats.active_workers.join(", ")
                }
            );
            for reason in stats.scheduler_reasons {
                println!("  {reason}");
            }
            Ok(())
        }
        JobCommand::Partitions(id) => {
            for partition in client().job_partitions(&id)? {
                println!(
                    "{} index={} units={} state={} node={} attempt={} duration={}us",
                    partition.id,
                    partition.index,
                    partition.units,
                    partition.state,
                    partition.node_id,
                    partition.attempt,
                    partition.execution_duration_us
                );
                if !partition.assignment_reason.is_empty() {
                    println!("  reason={}", partition.assignment_reason);
                }
            }
            Ok(())
        }
    }
}

fn job_action(id: &str, action: &str) -> Result<(), CommandError> {
    let job = client().job_action(id, action)?;
    println!("Job {}: {}", job.id, job.status);
    Ok(())
}

fn task_control(command: TaskCommand) -> Result<(), CommandError> {
    match command {
        TaskCommand::List => {
            let tasks = client().tasks()?;
            header("Nodren Tasks");
            if tasks.is_empty() {
                println!("No tasks found.");
            }
            for task in tasks {
                println!(
                    "{}  type={}  state={}  worker={}",
                    task.id,
                    task.task
                        .as_ref()
                        .map(|value| value.task_type.as_str())
                        .unwrap_or("-"),
                    task.status,
                    if task.node_id.is_empty() {
                        "-"
                    } else {
                        &task.node_id
                    }
                );
            }
            Ok(())
        }
        TaskCommand::Info(id) => {
            let task = client().task(&id)?;
            print_task(&task);
            Ok(())
        }
        TaskCommand::Cancel(id) => {
            let task = client().task_action(&id, "cancel")?;
            println!("Task {}: {}", task.id, task.status);
            Ok(())
        }
        TaskCommand::Retry(id) => {
            let task = client().task_action(&id, "retry")?;
            println!("Task {}: {}", task.id, task.status);
            Ok(())
        }
        TaskCommand::Logs(id) | TaskCommand::Result(id) => {
            let task = client().task(&id)?;
            if let Some(result) = task.execution_result {
                print_task_result(&result);
            } else {
                println!("Task {id} has no execution result yet.");
            }
            Ok(())
        }
    }
}

fn print_task(task: &crate::api::Job) {
    println!("Task ID      {}", task.id);
    println!("Status       {}", task.status);
    if let Some(spec) = &task.task {
        println!("Type         {}", spec.task_type);
        if !spec.executable.is_empty() {
            println!("Executable   {}", spec.executable);
        }
        if !spec.runtime.is_empty() {
            println!("Runtime      {}", spec.runtime);
        }
        if !spec.script.is_empty() {
            println!("Script       {}", spec.script);
        }
        if !spec.arguments.is_empty() {
            println!("Arguments    {}", spec.arguments.join(" "));
        }
    }
    if let Some(result) = &task.execution_result {
        print_task_result(result);
    }
}

fn print_task_result(result: &crate::api::GeneralTaskResult) {
    println!("Execution    {} ({} us)", result.status, result.duration_us);
    if let Some(exit_code) = result.exit_code {
        println!("Exit code    {exit_code}");
    }
    if !result.error.is_empty() {
        println!("Error        {} {}", result.error_code, result.error);
    }
    if let Ok(stdout) = BASE64.decode(&result.stdout_base64) {
        if !stdout.is_empty() {
            println!("Stdout:\n{}", String::from_utf8_lossy(&stdout));
        }
    }
    if let Ok(stderr) = BASE64.decode(&result.stderr_base64) {
        if !stderr.is_empty() {
            println!("Stderr:\n{}", String::from_utf8_lossy(&stderr));
        }
    }
    if result.stdout_truncated || result.stderr_truncated {
        println!(
            "Output truncated (stdout={} stderr={})",
            result.stdout_truncated, result.stderr_truncated
        );
    }
}

fn distribution(command: DistributionCommand) -> Result<(), CommandError> {
    match command {
        DistributionCommand::Show(Some(id)) => {
            let job = client().job(&id)?;
            println!(
                "Job {}\nMode: {}",
                job.id,
                job.distribution.mode.to_ascii_uppercase()
            );
            for (worker, percent) in job.distribution.manual_allocations {
                println!("{worker}\t{percent}%");
            }
            Ok(())
        }
        DistributionCommand::Show(None) => {
            for job in client().jobs()? {
                println!(
                    "{}  mode={}  progress={:.2}%",
                    job.id, job.distribution.mode, job.distribution.progress_percent
                );
            }
            Ok(())
        }
        DistributionCommand::Auto(id) => {
            let job = client().update_distribution(
                &id,
                &DistributionRequest {
                    mode: "automatic".into(),
                    manual_allocations: None,
                },
            )?;
            println!("Job {} distribution: AUTOMATIC", job.id);
            Ok(())
        }
        DistributionCommand::Set(id, values) => {
            let allocations = parse_allocations(&values)?;
            let job = client().update_distribution(
                &id,
                &DistributionRequest {
                    mode: "manual".into(),
                    manual_allocations: Some(allocations),
                },
            )?;
            println!("Job {} distribution: MANUAL", job.id);
            Ok(())
        }
    }
}

fn parse_allocations(
    values: &[String],
) -> Result<std::collections::HashMap<String, u8>, CommandError> {
    let mut allocations = std::collections::HashMap::new();
    for value in values {
        for entry in value.split(',') {
            let (worker, percent) = entry.split_once('=').ok_or_else(|| {
                CommandError::Usage(format!(
                    "invalid allocation '{entry}'; expected worker=percent"
                ))
            })?;
            let percent = percent.parse::<u8>().map_err(|_| {
                CommandError::Usage(format!("invalid allocation percentage '{percent}'"))
            })?;
            if percent > 100 || worker.trim().is_empty() {
                return Err(CommandError::Usage(format!("invalid allocation '{entry}'")));
            }
            allocations.insert(worker.trim().to_string(), percent);
        }
    }
    Ok(allocations)
}

// ---------------------------------------------------------
// Service lifecycle
// ---------------------------------------------------------

fn start() -> Result<(), CommandError> {
    Err(CommandError::Usage(
        "The Rust CLI does not supervise local processes; run the Controller entrypoint or use a process manager.".to_string(),
    ))
}

fn stop() -> Result<(), CommandError> {
    Err(CommandError::Usage(
        "Controller/Worker process lifecycle is local-process managed and is not exposed by the HTTP API.".to_string(),
    ))
}

fn restart() -> Result<(), CommandError> {
    Err(CommandError::Usage(
        "Controller/Worker process lifecycle is local-process managed and is not exposed by the HTTP API.".to_string(),
    ))
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

fn print_worker_stats(stats: &crate::api::WorkerStats) -> Result<(), CommandError> {
    let node = &stats.node;
    println!(
        "Worker {} state={} capacity={:.2} telemetry_age={}ms CPU={:.1}% active_tasks={} throughput={:.2} units/s factor={:.2}",
        node.info.id,
        node.state,
        stats.scheduler_weight,
        stats.telemetry_age_ms,
        node.telemetry.cpu_utilization_percent,
        node.telemetry.active_tasks,
        node.observed_throughput_units_per_second,
        node.performance_factor
    );
    if !stats.current_partitions.is_empty() {
        println!("  partitions={}", stats.current_partitions.join(", "));
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
            "Usage: nodren run <sum|xor|dot_product|process|command|script> [arguments...]"
                .to_string(),
        ));
    }

    let command = args[0].to_ascii_lowercase();
    if matches!(command.as_str(), "process" | "command" | "script") {
        return run_general_task(&command, &args[1..]);
    }
    let payload = workload_payload(&command, &args[1..])?;
    let request = JobRequest {
        command: command.clone(),
        priority: 50,
        requirements: ResourceRequirements {
            cpu_cores: 1,
            ram_gb: 1,
            max_ram_gb: 0,
            gpu_required: false,
            gpu_count: 0,
            vram_gb: 0,
            accelerator_type: String::new(),
            gpu_capabilities: Vec::new(),
        },
        payload_base64: Some(BASE64.encode(payload)),
        distribution_mode: None,
        manual_allocations: None,
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

fn run_general_task(command: &str, args: &[String]) -> Result<(), CommandError> {
    let separator = args
        .iter()
        .position(|value| value == "--")
        .unwrap_or(args.len());
    let program = &args[..separator];
    let task_arguments = if separator < args.len() {
        args[separator + 1..].to_vec()
    } else {
        Vec::new()
    };
    let mut spec = GeneralTaskSpec {
        task_type: command.to_ascii_uppercase(),
        version: "1".to_string(),
        arguments: task_arguments,
        requirements: ResourceRequirements {
            cpu_cores: 1,
            ram_gb: 1,
            max_ram_gb: 0,
            gpu_required: false,
            gpu_count: 0,
            vram_gb: 0,
            accelerator_type: String::new(),
            gpu_capabilities: Vec::new(),
        },
        retry: RetryPolicy { max_retries: 0 },
        ..GeneralTaskSpec::default()
    };
    match command {
        "script" if program.len() >= 2 => {
            spec.runtime = program[0].clone();
            spec.script = program[1].clone();
        }
        "script" => {
            return Err(CommandError::Usage(
                "Usage: nodren run script <runtime> <script> [-- arguments...]".to_string(),
            ));
        }
        "process" | "command" if program.len() == 1 => spec.executable = program[0].clone(),
        "process" | "command" => {
            return Err(CommandError::Usage(format!(
                "Usage: nodren run {command} <executable> [-- arguments...]"
            )));
        }
        _ => unreachable!(),
    }
    let submitted = client().submit_task(&TaskRequest {
        id: None,
        batch_id: None,
        task: spec,
    })?;
    println!("Task ID       {}", submitted.id);
    println!("Type          {}", command.to_ascii_uppercase());
    println!("State         {}", submitted.status);
    println!("Waiting for result...");
    let timeout = std::env::var("NODREN_JOB_TIMEOUT_SECS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(30);
    let completed = client().wait_for_job(&submitted.id, Duration::from_secs(timeout))?;
    let result = completed.execution_result.as_ref().ok_or_else(|| {
        ApiError::InvalidResponse("completed task has no execution result".to_string())
    })?;
    print_task_result(result);
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

fn print_job(job: &crate::api::Job) {
    let workers = if job.node_ids.is_empty() {
        if job.node_id.is_empty() {
            "-".to_string()
        } else {
            job.node_id.clone()
        }
    } else {
        job.node_ids.join(", ")
    };
    println!("Job ID       {}", job.id);
    println!("Workload     {}", job.command);
    println!("Status       {}", job.status);
    println!("Progress     {:.2}%", job.distribution.progress_percent);
    println!("Workers      {}", workers);
    println!(
        "Partitions   {} total, {} completed, {} running, {} pending",
        job.distribution.total_partitions,
        job.distribution.completed_partitions,
        job.distribution.running_partitions,
        job.distribution.pending_partitions
    );
    println!("Created      {}", job.created_at);
    println!("Updated      {}", job.updated_at);
    if let Some(result) = &job.result {
        println!("Result       {}", result.value);
        println!("Error        {} {}", result.error_code, result.error);
    }
}

fn logs(args: Vec<String>) -> Result<(), CommandError> {
    let target = if args.is_empty() {
        "the Controller API".to_string()
    } else {
        args.join(", ")
    };
    Err(CommandError::Usage(format!(
        "centralized log retrieval is not available for {target}"
    )))
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
    workers list             List registered workers
    workers stats            Show telemetry and scheduler capacity
    workers info <worker>   Show worker details
    workers ping <worker>   Check worker reachability
    workers pause <worker>  Stop new scheduling to a worker
    workers resume <worker> Resume scheduling to a worker
    workers remove <worker> Disconnect a worker

WORKLOADS
    run sum <byte>...       Submit and wait for a sum result
    run xor <byte>...       Submit and wait for an xor result
    run dot_product <left> <right>
                            Vectors are comma-separated integers
    run process <executable> [-- arguments...]
    run command <executable> [-- arguments...]
    run script <runtime> <script> [-- arguments...]
    jobs list               List live jobs
    jobs info <job>         Show job details and progress
    jobs stats <job>         Show timing and scheduler decisions
    jobs partitions <job>    Show partition states and assignment reasons
    jobs run <workload> ... Submit and wait for a result
    jobs cancel <job>       Cancel a job
    jobs pause <job>        Pause a queued job
    jobs resume <job>       Resume a paused job
    tasks list               List generalized tasks
    tasks info <task>        Show task details and execution result
    tasks logs <task>        Show captured stdout/stderr
    tasks result <task>      Show exit code and execution result
    tasks cancel <task>      Cancel a queued or running task
    tasks retry <task>       Retry a failed or timed-out task
    distribution show [job] Show distribution state
    distribution auto <job> Use automatic distribution
    distribution set <job> worker=percent...
    monitor                  Stream controller events (alias: events)

LIMITED
    start                   Not available; use the Controller entrypoint
    stop                    Not available through the HTTP API
    restart                 Not available through the HTTP API
    logs [target]           Not available through the HTTP API

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
