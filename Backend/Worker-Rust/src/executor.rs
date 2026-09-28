use crate::native_core::NativeCore;
use crate::protocol::{MessageType, NodeInfo, Task, encode_task_results, write_frame};
use crate::{can_run, execute_task, resource_failure_result};

use std::{
    collections::VecDeque,
    io,
    net::TcpStream,
    sync::{
        Arc, Condvar, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::{self, JoinHandle},
};

struct WorkItem {
    task: Task,
    request_id: u64,
    writer: Arc<Mutex<TcpStream>>,
    connection_failed: Arc<AtomicBool>,
}

struct QueueState {
    tasks: VecDeque<WorkItem>,
    stopping: bool,
}

struct WorkQueue {
    state: Mutex<QueueState>,
    available: Condvar,
    space: Condvar,
    capacity: usize,
}

impl WorkQueue {
    fn new(capacity: usize) -> Self {
        Self {
            state: Mutex::new(QueueState {
                tasks: VecDeque::new(),
                stopping: false,
            }),
            available: Condvar::new(),
            space: Condvar::new(),
            capacity,
        }
    }

    fn push(&self, item: WorkItem) -> io::Result<()> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| io::Error::other("worker queue mutex poisoned"))?;
        while state.tasks.len() >= self.capacity && !state.stopping {
            state = self
                .space
                .wait(state)
                .map_err(|_| io::Error::other("worker queue mutex poisoned"))?;
        }
        if state.stopping {
            return Err(io::Error::new(
                io::ErrorKind::BrokenPipe,
                "worker executor stopped",
            ));
        }
        state.tasks.push_back(item);
        self.available.notify_one();
        Ok(())
    }

    fn pop(&self) -> Option<WorkItem> {
        let mut state = self.state.lock().ok()?;
        loop {
            if let Some(item) = state.tasks.pop_front() {
                self.space.notify_one();
                return Some(item);
            }
            if state.stopping {
                return None;
            }
            state = self.available.wait(state).ok()?;
        }
    }

    fn stop(&self) {
        if let Ok(mut state) = self.state.lock() {
            state.stopping = true;
            self.available.notify_all();
            self.space.notify_all();
        }
    }
}

struct ResourceState {
    used_cpu: u32,
    used_ram: u64,
}

struct ResourceGate {
    state: Mutex<ResourceState>,
    available: Condvar,
    total_cpu: u32,
    total_ram: u64,
}

struct ResourcePermit {
    gate: Arc<ResourceGate>,
    cpu: u32,
    ram: u64,
}

impl ResourceGate {
    fn new(node: &NodeInfo) -> Self {
        Self {
            state: Mutex::new(ResourceState {
                used_cpu: 0,
                used_ram: 0,
            }),
            available: Condvar::new(),
            total_cpu: node.cpu_cores.max(1),
            total_ram: node.ram_gb,
        }
    }

    fn acquire(self: &Arc<Self>, task: &Task) -> Option<ResourcePermit> {
        let cpu = task.requirements.cpu_cores.max(1);
        let ram = task.requirements.ram_gb;
        if cpu > self.total_cpu || ram > self.total_ram {
            return None;
        }

        let mut state = self.state.lock().ok()?;
        while state.used_cpu + cpu > self.total_cpu || state.used_ram + ram > self.total_ram {
            state = self.available.wait(state).ok()?;
        }
        state.used_cpu += cpu;
        state.used_ram += ram;
        Some(ResourcePermit {
            gate: Arc::clone(self),
            cpu,
            ram,
        })
    }
}

impl Drop for ResourcePermit {
    fn drop(&mut self) {
        if let Ok(mut state) = self.gate.state.lock() {
            state.used_cpu = state.used_cpu.saturating_sub(self.cpu);
            state.used_ram = state.used_ram.saturating_sub(self.ram);
            self.gate.available.notify_all();
        }
    }
}

pub struct WorkerExecutor {
    queue: Arc<WorkQueue>,
    workers: Vec<JoinHandle<()>>,
}

impl WorkerExecutor {
    pub fn new(node: NodeInfo, core: Arc<NativeCore>) -> Self {
        let worker_count = node.cpu_cores.max(1) as usize;
        let queue = Arc::new(WorkQueue::new(worker_count.saturating_mul(4).max(16)));
        let gate = Arc::new(ResourceGate::new(&node));
        let mut workers = Vec::with_capacity(worker_count);

        for index in 0..worker_count {
            let queue_ref = Arc::clone(&queue);
            let gate_ref = Arc::clone(&gate);
            let node_ref = node.clone();
            let core_ref = Arc::clone(&core);
            let name = format!("nodren-task-{index}");
            workers.push(
                thread::Builder::new()
                    .name(name)
                    .spawn(move || {
                        while let Some(item) = queue_ref.pop() {
                            let result = if !can_run(&node_ref, &item.task) {
                                resource_failure_result(&node_ref, &item.task)
                            } else if let Some(_permit) = gate_ref.acquire(&item.task) {
                                execute_task(&node_ref, &core_ref, &item.task)
                            } else {
                                resource_failure_result(&node_ref, &item.task)
                            };

                            let payload = match encode_task_results(&[result]) {
                                Ok(payload) => payload,
                                Err(_) => {
                                    item.connection_failed.store(true, Ordering::Release);
                                    continue;
                                }
                            };
                            let write_result = item
                                .writer
                                .lock()
                                .map_err(|_| io::Error::other("worker writer mutex poisoned"))
                                .and_then(|mut writer| {
                                    write_frame(
                                        &mut *writer,
                                        MessageType::TaskResultBatch,
                                        item.request_id,
                                        &payload,
                                    )
                                });
                            if write_result.is_err() {
                                item.connection_failed.store(true, Ordering::Release);
                            }
                        }
                    })
                    .expect("failed to start Nodren worker executor thread"),
            );
        }

        Self { queue, workers }
    }

    pub fn submit(
        &self,
        task: Task,
        request_id: u64,
        writer: Arc<Mutex<TcpStream>>,
        connection_failed: Arc<AtomicBool>,
    ) -> io::Result<()> {
        self.queue.push(WorkItem {
            task,
            request_id,
            writer,
            connection_failed,
        })
    }
}

impl Drop for WorkerExecutor {
    fn drop(&mut self) {
        self.queue.stop();
        for worker in self.workers.drain(..) {
            let _ = worker.join();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn task(cpu_cores: u32, ram_gb: u64) -> Task {
        Task {
            id: 1,
            job_id: "job-1".to_string(),
            command: "sum".to_string(),
            priority: 50,
            requirements: crate::protocol::ResourceRequirements {
                cpu_cores,
                ram_gb,
                gpu_required: false,
            },
            payload: vec![1],
        }
    }

    #[test]
    fn resource_gate_limits_cpu_and_releases_capacity() {
        let gate = Arc::new(ResourceGate::new(&NodeInfo {
            id: "node-1".to_string(),
            hostname: "host".to_string(),
            os: "test".to_string(),
            arch: "test".to_string(),
            cpu_cores: 2,
            ram_gb: 4,
            gpu: crate::protocol::GPUInfo {
                vendor: String::new(),
                model: String::new(),
                vram_gb: 0,
            },
        }));

        let first = gate.acquire(&task(1, 1)).expect("first permit");
        let second = gate.acquire(&task(1, 1)).expect("second permit");
        assert!(gate.acquire(&task(3, 1)).is_none());
        drop(first);
        drop(second);
        assert!(gate.acquire(&task(2, 4)).is_some());
    }
}
