#[derive(Debug, Clone)]
pub struct ResourceRequirements {
    pub cpu_cores: u8,
    pub ram_gb: u8,
    pub gpu_required: bool,
}

#[derive(Debug, Clone)]
pub struct NodeInfo {
    pub id: String,
    pub host: String,
    pub os: String,
    pub arch: String,
    pub cpu_cores: u8,
    pub ram_gb: u8,
    pub gpu: String,
}

#[derive(Debug, Clone)]
pub struct JobRequest {
    pub id: String,
    pub command: String,
    pub priority: u8,
    pub requirements: ResourceRequirements,
}

#[derive(Debug, Clone)]
pub struct JobResult {
    pub job_id: String,
    pub worker_id: String,
    pub status: String,
    pub output: String,
}

pub fn default_requirements() -> ResourceRequirements {
    ResourceRequirements {
        cpu_cores: 1,
        ram_gb: 2,
        gpu_required: false,
    }
}
