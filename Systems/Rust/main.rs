#[derive(Debug)]
struct ResourceRequirements {
    cpu_cores: u8,
    ram_gb: u8,
    gpu_required: bool,
}

#[derive(Debug)]
struct NodeInfo {
    id: String,
    host: String,
    os: String,
    arch: String,
    cpu_cores: u8,
    ram_gb: u8,
    gpu: String,
}

#[derive(Debug)]
struct JobRequest {
    id: String,
    command: String,
    priority: u8,
    requirements: ResourceRequirements,
}

fn register_worker(node: &NodeInfo) {
    println!("[worker] registered: {} on {} {}", node.id, node.os, node.arch);
}

fn execute_job(job: &JobRequest) {
    println!("[worker] executing {} with priority {}", job.id, job.priority);
}

fn main() {
    let node = NodeInfo {
        id: "worker-02".to_string(),
        host: "node-02".to_string(),
        os: "Linux".to_string(),
        arch: "arm64".to_string(),
        cpu_cores: 8,
        ram_gb: 32,
        gpu: "NVIDIA".to_string(),
    };

    let job = JobRequest {
        id: "JOB-1847".to_string(),
        command: "simulation".to_string(),
        priority: 5,
        requirements: ResourceRequirements {
            cpu_cores: 4,
            ram_gb: 8,
            gpu_required: false,
        },
    };

    register_worker(&node);
    execute_job(&job);
    println!("[worker] NEXUS worker online");
}
