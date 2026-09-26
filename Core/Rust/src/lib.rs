use serde::{Deserialize, Serialize};
use std::sync::{Arc, Mutex};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskEnvelope {
    pub id: String,
    pub kind: String,
    pub payload: Vec<i32>,
    pub priority: u8,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct RuntimeStatus {
    pub initialized: bool,
    pub cpu_cores: usize,
    pub memory_mb: u64,
    pub platform: String,
    pub queued_tasks: usize,
    pub completed_tasks: usize,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct TaskResult {
    pub task_id: String,
    pub value: i64,
    pub ok: bool,
    pub message: String,
}

#[derive(Debug)]
pub struct CoreRuntime {
    initialized: bool,
    cpu_cores: usize,
    memory_mb: u64,
    platform: String,
    queue: Vec<TaskEnvelope>,
    completed: usize,
}

impl Default for CoreRuntime {
    fn default() -> Self {
        Self {
            initialized: false,
            cpu_cores: 1,
            memory_mb: 0,
            platform: "unknown".to_string(),
            queue: Vec::new(),
            completed: 0,
        }
    }
}

impl CoreRuntime {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn initialize(&mut self, cpu_cores: usize, memory_mb: u64, platform: &str) -> Result<(), String> {
        if cpu_cores == 0 {
            return Err("cpu_cores must be greater than zero".to_string());
        }

        self.initialized = true;
        self.cpu_cores = cpu_cores;
        self.memory_mb = memory_mb;
        self.platform = platform.to_string();
        Ok(())
    }

    pub fn submit_task(&mut self, task: TaskEnvelope) -> Result<TaskResult, String> {
        if !self.initialized {
            return Err("runtime is not initialized".to_string());
        }

        self.queue.push(task.clone());

        let mut total: i64 = 0;
        for value in &task.payload {
            total += i64::from(*value);
        }

        self.completed += 1;
        let result = TaskResult {
            task_id: task.id,
            value: total,
            ok: true,
            message: "task completed".to_string(),
        };

        Ok(result)
    }

    pub fn status(&self) -> RuntimeStatus {
        RuntimeStatus {
            initialized: self.initialized,
            cpu_cores: self.cpu_cores,
            memory_mb: self.memory_mb,
            platform: self.platform.clone(),
            queued_tasks: self.queue.len(),
            completed_tasks: self.completed,
        }
    }

    pub fn shutdown(&mut self) {
        self.queue.clear();
        self.initialized = false;
        self.completed = 0;
    }
}

static RUNTIME: std::sync::LazyLock<Arc<Mutex<CoreRuntime>>> =
    std::sync::LazyLock::new(|| Arc::new(Mutex::new(CoreRuntime::new())));

#[no_mangle]
pub extern "C" fn crazyapp_runtime_init(cpu_cores: usize, memory_mb: u64, platform: *const std::ffi::c_char) -> i32 {
    let c_str = unsafe { std::ffi::CStr::from_ptr(platform) };
    let platform = c_str.to_string_lossy();

    let mut runtime = RUNTIME.lock().unwrap();
    match runtime.initialize(cpu_cores, memory_mb, &platform) {
        Ok(()) => 0,
        Err(_) => -1,
    }
}

#[no_mangle]
pub extern "C" fn crazyapp_runtime_submit_task(payload: *const u8, len: usize) -> i64 {
    if payload.is_null() || len == 0 {
        return -1;
    }

    let slice = unsafe { std::slice::from_raw_parts(payload, len) };
    let text = match std::str::from_utf8(slice) {
        Ok(value) => value,
        Err(_) => return -1,
    };

    let task: TaskEnvelope = match serde_json::from_str(text) {
        Ok(value) => value,
        Err(_) => return -1,
    };

    let mut runtime = RUNTIME.lock().unwrap();
    match runtime.submit_task(task) {
        Ok(result) => result.value,
        Err(_) => -1,
    }
}

#[no_mangle]
pub extern "C" fn crazyapp_runtime_shutdown() {
    let mut runtime = RUNTIME.lock().unwrap();
    runtime.shutdown();
}

#[no_mangle]
pub extern "C" fn crazyapp_runtime_status() -> usize {
    let runtime = RUNTIME.lock().unwrap();
    runtime.status().queued_tasks
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn initializes_and_executes_task() {
        let mut runtime = CoreRuntime::new();
        runtime.initialize(8, 4096, "windows").unwrap();

        let task = TaskEnvelope {
            id: "task-rust-01".to_string(),
            kind: "sum".to_string(),
            payload: vec![4, 8, 15, 16, 23, 42],
            priority: 3,
        };

        let result = runtime.submit_task(task).unwrap();
        assert!(result.ok);
        assert_eq!(result.value, 108);
        assert_eq!(runtime.status().completed_tasks, 1);
        runtime.shutdown();
    }
}
