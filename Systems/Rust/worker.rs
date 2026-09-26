#[derive(Debug)]
pub struct WorkerStatus {
    pub id: String,
    pub health: String,
    pub cpu_cores: u8,
    pub ram_gb: u8,
    pub gpu: String,
}

pub fn report_status(status: &WorkerStatus) {
    println!("[worker] {} health={} cores={} ram={}GB", status.id, status.health, status.cpu_cores, status.ram_gb);
}

fn main() {
    let status = WorkerStatus {
        id: "worker-03".to_string(),
        health: "online".to_string(),
        cpu_cores: 16,
        ram_gb: 64,
        gpu: "AMD".to_string(),
    };

    report_status(&status);
}
