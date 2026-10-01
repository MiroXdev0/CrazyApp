use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Deserialize)]
pub struct Health {
    pub status: String,
    pub service: String,
    pub version: String,
    pub nodes: usize,
}

#[derive(Debug, Clone, Default, Deserialize)]
pub struct GpuInfo {
    #[serde(default)]
    pub vendor: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub vram_gb: u64,
    #[serde(default)]
    pub count: u32,
    #[serde(default)]
    pub capabilities: Vec<String>,
    #[serde(default)]
    pub driver: String,
    #[serde(default)]
    pub runtime: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct NodeInfo {
    pub id: String,
    #[serde(default)]
    pub hostname: String,
    #[serde(default)]
    pub os: String,
    #[serde(default)]
    pub arch: String,
    #[serde(default)]
    pub cpu_model: String,
    #[serde(default)]
    pub cpu_cores: u32,
    #[serde(default)]
    pub ram_gb: u64,
    #[serde(default)]
    pub gpu: GpuInfo,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize)]
pub struct NodeRecord {
    pub info: NodeInfo,
    pub state: String,
    #[serde(default)]
    pub assigned_jobs: Vec<String>,
    #[serde(default)]
    pub allocated_cpu_cores: u32,
    #[serde(default)]
    pub allocated_ram_gb: u64,
    #[serde(default)]
    pub allocated_gpu_count: u32,
    #[serde(default)]
    pub allocated_vram_gb: u64,
    #[serde(default)]
    pub available_cpu_cores: u32,
    #[serde(default)]
    pub available_ram_gb: u64,
    #[serde(default)]
    pub capacity_score: f64,
    #[serde(default)]
    pub effective_capacity: f64,
    #[serde(default)]
    pub telemetry: WorkerTelemetry,
    #[serde(default)]
    pub completed_tasks: u64,
    #[serde(default)]
    pub failed_tasks: u64,
    #[serde(default)]
    pub total_execution_us: u64,
    #[serde(default)]
    pub observed_throughput_units_per_second: f64,
    #[serde(default)]
    pub performance_factor: f64,
    #[serde(default)]
    pub last_heartbeat: String,
    #[serde(default)]
    pub connected_at: String,
}

#[derive(Debug, Clone, Default, Deserialize)]
pub struct WorkerTelemetry {
    #[serde(default)]
    pub timestamp: String,
    #[serde(default)]
    pub uptime_seconds: u64,
    #[serde(default)]
    pub active_tasks: u32,
    #[serde(default)]
    pub cpu_utilization_percent: f64,
    #[serde(default)]
    pub memory_available_gb: u64,
    #[serde(default)]
    pub memory_utilization_percent: f64,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct ResourceRequirements {
    pub cpu_cores: u32,
    pub ram_gb: u64,
    #[serde(default)]
    pub max_ram_gb: u64,
    pub gpu_required: bool,
    #[serde(default)]
    pub gpu_count: u32,
    #[serde(default)]
    pub vram_gb: u64,
    #[serde(default)]
    pub accelerator_type: String,
    #[serde(default)]
    pub gpu_capabilities: Vec<String>,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Default, Deserialize)]
pub struct DistributionInfo {
    #[serde(default)]
    pub mode: String,
    #[serde(default)]
    pub partitionable: bool,
    #[serde(default)]
    pub total_partitions: usize,
    #[serde(default)]
    pub completed_partitions: usize,
    #[serde(default)]
    pub running_partitions: usize,
    #[serde(default)]
    pub pending_partitions: usize,
    #[serde(default)]
    pub failed_partitions: usize,
    #[serde(default)]
    pub requeued_partitions: usize,
    #[serde(default)]
    pub total_units: u64,
    #[serde(default)]
    pub completed_units: u64,
    #[serde(default)]
    pub progress_percent: f64,
    #[serde(default)]
    pub manual_allocations: std::collections::HashMap<String, u8>,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize)]
pub struct JobResult {
    #[serde(default)]
    pub task_id: u64,
    pub job_id: String,
    pub status: String,
    #[serde(default)]
    pub value: i64,
    #[serde(default)]
    pub error_code: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub duration_us: u64,
    #[serde(default)]
    pub node_id: String,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize)]
pub struct Job {
    pub id: String,
    pub command: String,
    #[serde(default)]
    pub priority: u8,
    pub requirements: ResourceRequirements,
    #[serde(default)]
    pub payload_base64: String,
    pub status: String,
    #[serde(default)]
    pub node_id: String,
    #[serde(default)]
    pub node_ids: Vec<String>,
    pub result: Option<JobResult>,
    #[serde(default)]
    pub distribution: DistributionInfo,
    #[serde(default)]
    pub created_at: String,
    #[serde(default)]
    pub updated_at: String,
    #[serde(default)]
    pub started_at: Option<String>,
    #[serde(default)]
    pub completed_at: Option<String>,
    #[serde(default)]
    pub queue_time_ms: i64,
    #[serde(default)]
    pub elapsed_ms: i64,
    #[serde(default)]
    pub partitions: Vec<Partition>,
    #[serde(default)]
    pub task: Option<GeneralTaskSpec>,
    #[serde(default)]
    pub execution_result: Option<GeneralTaskResult>,
    #[serde(default)]
    pub batch_id: String,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct GeneralTaskSpec {
    #[serde(rename = "type")]
    pub task_type: String,
    pub version: String,
    #[serde(default)]
    pub executable: String,
    #[serde(default)]
    pub runtime: String,
    #[serde(default)]
    pub script: String,
    #[serde(default)]
    pub workload: String,
    #[serde(default)]
    pub arguments: Vec<String>,
    #[serde(default)]
    pub environment: std::collections::HashMap<String, String>,
    #[serde(default)]
    pub working_directory: String,
    #[serde(default)]
    pub stdin_base64: String,
    #[serde(default)]
    pub input_artifacts: Vec<TaskArtifact>,
    #[serde(default)]
    pub output_artifacts: Vec<TaskArtifact>,
    #[serde(default)]
    pub timeout_ms: u64,
    #[serde(default)]
    pub stdout_limit_bytes: u64,
    #[serde(default)]
    pub stderr_limit_bytes: u64,
    pub requirements: ResourceRequirements,
    #[serde(default)]
    pub target: TaskTarget,
    #[serde(default)]
    pub retry: RetryPolicy,
    #[serde(default)]
    pub workload_kind: String,
    #[serde(default)]
    pub strategy: String,
    #[serde(default)]
    pub required_workers: u32,
    #[serde(default)]
    pub replicas: u32,
    #[serde(default)]
    pub package_manifest: Option<TaskPackageManifest>,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct TaskArtifact {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub size: u64,
    #[serde(default)]
    pub sha256: String,
    #[serde(default)]
    pub kind: String,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct TaskPackageManifest {
    #[serde(default)]
    pub entry_point: String,
    #[serde(default)]
    pub runtime: String,
    #[serde(default)]
    pub arguments: Vec<String>,
    #[serde(default)]
    pub environment: std::collections::HashMap<String, String>,
    #[serde(default)]
    pub os: String,
    #[serde(default)]
    pub arch: String,
    #[serde(default)]
    pub required_runtimes: Vec<String>,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct TaskTarget {
    #[serde(default)]
    pub os: String,
    #[serde(default)]
    pub arch: String,
    #[serde(default)]
    pub required_runtimes: Vec<String>,
    #[serde(default)]
    pub required_capabilities: Vec<String>,
    #[serde(default)]
    pub allowed_worker_ids: Vec<String>,
    #[serde(default)]
    pub preferred_worker_id: String,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct RetryPolicy {
    #[serde(default)]
    pub max_retries: u32,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct GeneralTaskResult {
    pub status: String,
    #[serde(default)]
    pub exit_code: Option<i32>,
    #[serde(default)]
    pub stdout_base64: String,
    #[serde(default)]
    pub stderr_base64: String,
    #[serde(default)]
    pub stdout_truncated: bool,
    #[serde(default)]
    pub stderr_truncated: bool,
    #[serde(default)]
    pub duration_us: u64,
    #[serde(default)]
    pub error_code: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub attempt: u32,
}

#[derive(Debug, Clone, Default, Deserialize)]
pub struct Partition {
    pub id: String,
    #[serde(default)]
    pub index: usize,
    #[serde(default)]
    pub units: u64,
    pub state: String,
    #[serde(default)]
    pub task_id: u64,
    #[serde(default)]
    pub node_id: String,
    #[serde(default)]
    pub attempt: u32,
    #[serde(default)]
    pub execution_duration_us: u64,
    #[serde(default)]
    pub assignment_reason: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkerStats {
    pub node: NodeRecord,
    #[serde(default)]
    pub current_partitions: Vec<String>,
    #[serde(default)]
    pub telemetry_age_ms: i64,
    #[serde(default)]
    pub scheduler_weight: f64,
}

#[derive(Debug, Clone, Deserialize)]
pub struct JobStats {
    pub job: Job,
    #[serde(default)]
    pub queue_time_ms: i64,
    #[serde(default)]
    pub elapsed_ms: i64,
    #[serde(default)]
    pub active_workers: Vec<String>,
    #[serde(default)]
    pub scheduler_reasons: Vec<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct JobRequest {
    pub command: String,
    pub priority: u8,
    pub requirements: ResourceRequirements,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub payload_base64: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub distribution_mode: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub manual_allocations: Option<std::collections::HashMap<String, u8>>,
}

#[derive(Debug, Clone, Serialize)]
pub struct TaskRequest {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub batch_id: Option<String>,
    pub task: GeneralTaskSpec,
}

#[derive(Debug, Clone, Serialize)]
pub struct DistributionRequest {
    pub mode: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub manual_allocations: Option<std::collections::HashMap<String, u8>>,
}

#[cfg(test)]
mod tests {
    use super::{Job, JobRequest, NodeRecord, ResourceRequirements};

    #[test]
    fn serializes_job_request_in_controller_shape() {
        let request = JobRequest {
            command: "sum".to_string(),
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
            payload_base64: Some("AQID".to_string()),
            distribution_mode: None,
            manual_allocations: None,
        };
        let json = serde_json::to_value(request).unwrap();
        assert_eq!(json["command"], "sum");
        assert_eq!(json["requirements"]["cpu_cores"], 1);
        assert_eq!(json["payload_base64"], "AQID");
    }

    #[test]
    fn parses_controller_node_response() {
        let node: NodeRecord = serde_json::from_str(
            r#"{
                "info":{"id":"NODE-01","hostname":"host","os":"windows","arch":"x86_64","cpu_cores":4,"ram_gb":8,"gpu":{"vendor":"","model":"","vram_gb":0}},
                "state":"READY","assigned_jobs":[],"allocated_cpu_cores":1,"allocated_ram_gb":2,
                "last_heartbeat":"2026-09-28T12:00:00Z","connected_at":"2026-09-28T11:00:00Z"
            }"#,
        )
        .unwrap();
        assert_eq!(node.info.id, "NODE-01");
        assert_eq!(node.info.cpu_cores, 4);
        assert_eq!(node.allocated_ram_gb, 2);
    }

    #[test]
    fn parses_completed_job_response() {
        let job: Job = serde_json::from_str(
            r#"{
                "id":"job-1","command":"sum","priority":50,
                "requirements":{"cpu_cores":1,"ram_gb":1,"gpu_required":false},
                "payload_base64":"AQID","status":"COMPLETED","node_id":"NODE-01",
                "result":{"task_id":1,"job_id":"job-1","status":"COMPLETED","value":6,"error_code":"","error":"","duration_us":5,"node_id":"NODE-01"},
                "created_at":"2026-09-28T12:00:00Z","updated_at":"2026-09-28T12:00:01Z"
            }"#,
        )
        .unwrap();
        assert_eq!(job.status, "COMPLETED");
        assert_eq!(job.result.unwrap().value, 6);
    }
}
