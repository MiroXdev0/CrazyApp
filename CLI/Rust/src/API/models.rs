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
    pub cpu_cores: u32,
    #[serde(default)]
    pub ram_gb: u64,
    #[serde(default)]
    pub gpu: GpuInfo,
}

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
    pub last_heartbeat: String,
    #[serde(default)]
    pub connected_at: String,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct ResourceRequirements {
    pub cpu_cores: u32,
    pub ram_gb: u64,
    pub gpu_required: bool,
}

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
    pub result: Option<JobResult>,
    #[serde(default)]
    pub created_at: String,
    #[serde(default)]
    pub updated_at: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct JobRequest {
    pub command: String,
    pub priority: u8,
    pub requirements: ResourceRequirements,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub payload_base64: Option<String>,
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
                gpu_required: false,
            },
            payload_base64: Some("AQID".to_string()),
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
