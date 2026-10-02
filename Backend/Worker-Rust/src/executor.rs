use crate::hashing::sha256_file;
use crate::native_core::NativeCore;
use crate::process_executor;
use crate::protocol::{
    ArtifactBegin, ArtifactChunk, ArtifactSpec, GeneralTaskEnvelope, MessageType, NodeInfo, Task,
    encode_artifact_begin, encode_artifact_chunk, encode_artifact_end, encode_general_task_result,
    encode_task_output, encode_task_results, write_frame,
};
use crate::{can_run, execute_task, resource_failure_result};

use std::{
    collections::{HashMap, VecDeque},
    fs::{self, File, OpenOptions},
    io::{self, Read, Seek, SeekFrom, Write},
    net::TcpStream,
    path::PathBuf,
    sync::{
        Arc, Condvar, Mutex,
        atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering},
    },
    thread::{self, JoinHandle},
};

struct WorkItem {
    task: Task,
    request_id: u64,
    writer: Arc<Mutex<TcpStream>>,
    connection_failed: Arc<AtomicBool>,
}

struct StoredArtifact {
    spec: ArtifactSpec,
    path: PathBuf,
    next_offset: u64,
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
    used_vram: u64,
}

struct ResourceGate {
    state: Mutex<ResourceState>,
    available: Condvar,
    total_cpu: u32,
    total_ram: u64,
    total_vram: u64,
}

struct ResourcePermit {
    gate: Arc<ResourceGate>,
    cpu: u32,
    ram: u64,
    vram: u64,
}

impl ResourceGate {
    fn new(node: &NodeInfo) -> Self {
        Self {
            state: Mutex::new(ResourceState {
                used_cpu: 0,
                used_ram: 0,
                used_vram: 0,
            }),
            available: Condvar::new(),
            total_cpu: node.cpu_cores.max(1),
            total_ram: node.ram_gb,
            total_vram: node.gpu.vram_gb,
        }
    }

    fn acquire_requirements(
        self: &Arc<Self>,
        requested_cpu: u32,
        ram: u64,
        vram: u64,
    ) -> Option<ResourcePermit> {
        let cpu = requested_cpu.max(1);
        if cpu > self.total_cpu || ram > self.total_ram || vram > self.total_vram {
            return None;
        }

        let mut state = self.state.lock().ok()?;
        while state.used_cpu + cpu > self.total_cpu
            || state.used_ram + ram > self.total_ram
            || state.used_vram + vram > self.total_vram
        {
            state = self.available.wait(state).ok()?;
        }
        state.used_cpu += cpu;
        state.used_ram += ram;
        state.used_vram += vram;
        Some(ResourcePermit {
            gate: Arc::clone(self),
            cpu,
            ram,
            vram,
        })
    }

    fn acquire(self: &Arc<Self>, task: &Task) -> Option<ResourcePermit> {
        self.acquire_requirements(
            task.requirements.cpu_cores,
            task.requirements.ram_gb,
            task.requirements.vram_gb,
        )
    }
}

impl Drop for ResourcePermit {
    fn drop(&mut self) {
        if let Ok(mut state) = self.gate.state.lock() {
            state.used_cpu = state.used_cpu.saturating_sub(self.cpu);
            state.used_ram = state.used_ram.saturating_sub(self.ram);
            state.used_vram = state.used_vram.saturating_sub(self.vram);
            self.gate.available.notify_all();
        }
    }
}

pub struct WorkerExecutor {
    queue: Arc<WorkQueue>,
    workers: Vec<JoinHandle<()>>,
    active_tasks: Arc<AtomicU32>,
    completed_tasks: Arc<AtomicU64>,
    failed_tasks: Arc<AtomicU64>,
    resource_gate: Arc<ResourceGate>,
    artifact_pending: Arc<Mutex<HashMap<(u64, String), StoredArtifact>>>,
    artifact_ready: Arc<Mutex<HashMap<(u64, String), StoredArtifact>>>,
    general_cancel: Arc<Mutex<HashMap<u64, Arc<AtomicBool>>>>,
    general_threads: Mutex<Vec<JoinHandle<()>>>,
}

const MAX_ARTIFACT_SIZE: u64 = 4 * 1024 * 1024 * 1024;

impl WorkerExecutor {
    pub fn new(node: NodeInfo, core: Arc<NativeCore>, worker_count: usize) -> Self {
        let worker_count = worker_count.clamp(1, node.cpu_cores.max(1) as usize);
        let queue = Arc::new(WorkQueue::new(worker_count.saturating_mul(4).max(16)));
        let gate = Arc::new(ResourceGate::new(&node));
        let active_tasks = Arc::new(AtomicU32::new(0));
        let completed_tasks = Arc::new(AtomicU64::new(0));
        let failed_tasks = Arc::new(AtomicU64::new(0));
        let general_cancel = Arc::new(Mutex::new(HashMap::new()));
        let mut workers = Vec::with_capacity(worker_count);

        for index in 0..worker_count {
            let queue_ref = Arc::clone(&queue);
            let gate_ref = Arc::clone(&gate);
            let node_ref = node.clone();
            let core_ref = Arc::clone(&core);
            let active_ref = Arc::clone(&active_tasks);
            let completed_ref = Arc::clone(&completed_tasks);
            let failed_ref = Arc::clone(&failed_tasks);
            let name = format!("nodren-task-{index}");
            workers.push(
                thread::Builder::new()
                    .name(name)
                    .spawn(move || {
                        while let Some(item) = queue_ref.pop() {
                            active_ref.fetch_add(1, Ordering::AcqRel);
                            let result = if !can_run(&node_ref, &item.task) {
                                resource_failure_result(&node_ref, &item.task)
                            } else if let Some(_permit) = gate_ref.acquire(&item.task) {
                                execute_task(&node_ref, &core_ref, &item.task)
                            } else {
                                resource_failure_result(&node_ref, &item.task)
                            };

                            record_task_outcome(&result.status, &completed_ref, &failed_ref);

                            let payload = match encode_task_results(&[result]) {
                                Ok(payload) => payload,
                                Err(_) => {
                                    item.connection_failed.store(true, Ordering::Release);
                                    active_ref.fetch_sub(1, Ordering::AcqRel);
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
                            active_ref.fetch_sub(1, Ordering::AcqRel);
                        }
                    })
                    .expect("failed to start Nodren worker executor thread"),
            );
        }

        Self {
            queue,
            workers,
            active_tasks,
            completed_tasks,
            failed_tasks,
            resource_gate: gate,
            artifact_pending: Arc::new(Mutex::new(HashMap::new())),
            artifact_ready: Arc::new(Mutex::new(HashMap::new())),
            general_cancel,
            general_threads: Mutex::new(Vec::new()),
        }
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

    pub fn active_tasks(&self) -> u32 {
        self.active_tasks.load(Ordering::Acquire)
    }

    pub fn active_tasks_counter(&self) -> Arc<AtomicU32> {
        Arc::clone(&self.active_tasks)
    }

    pub fn completed_tasks_counter(&self) -> Arc<AtomicU64> {
        Arc::clone(&self.completed_tasks)
    }

    pub fn failed_tasks_counter(&self) -> Arc<AtomicU64> {
        Arc::clone(&self.failed_tasks)
    }

    pub fn submit_general(
        &self,
        task: GeneralTaskEnvelope,
        request_id: u64,
        node: NodeInfo,
        writer: Arc<Mutex<TcpStream>>,
        connection_failed: Arc<AtomicBool>,
    ) -> io::Result<()> {
        if let Err(error) = validate_general_task(&node, &task) {
            self.failed_tasks.fetch_add(1, Ordering::AcqRel);
            let result = crate::protocol::GeneralTaskResult {
                task_id: task.task_id,
                job_id: task.job_id.clone(),
                attempt: task.attempt,
                status: "FAILED".to_string(),
                exit_code: None,
                stdout: Vec::new(),
                stderr: Vec::new(),
                stdout_truncated: false,
                stderr_truncated: false,
                duration_us: 0,
                error_code: "resource_requirements".to_string(),
                error: error.to_string(),
            };
            let payload = encode_general_task_result(&result)?;
            writer
                .lock()
                .map_err(|_| io::Error::other("worker writer mutex poisoned"))
                .and_then(|mut writer| {
                    write_frame(&mut *writer, MessageType::TaskResult, request_id, &payload)
                })?;
            return Ok(());
        }
        let mut input_artifacts = HashMap::new();
        for artifact in &task.spec.input_artifacts {
            let stored = match self.take_artifact(task.task_id, &artifact.id) {
                Some(stored) => stored,
                None => {
                    self.failed_tasks.fetch_add(1, Ordering::AcqRel);
                    let result = crate::protocol::GeneralTaskResult {
                        task_id: task.task_id,
                        job_id: task.job_id.clone(),
                        attempt: task.attempt,
                        status: "FAILED".to_string(),
                        exit_code: None,
                        stdout: Vec::new(),
                        stderr: Vec::new(),
                        stdout_truncated: false,
                        stderr_truncated: false,
                        duration_us: 0,
                        error_code: "artifact_not_ready".to_string(),
                        error: format!("input artifact {} is not ready", artifact.id),
                    };
                    let payload = encode_general_task_result(&result)?;
                    writer
                        .lock()
                        .map_err(|_| io::Error::other("worker writer mutex poisoned"))
                        .and_then(|mut writer| {
                            write_frame(&mut *writer, MessageType::TaskResult, request_id, &payload)
                        })?;
                    return Ok(());
                }
            };
            input_artifacts.insert(artifact.name.clone(), stored.path);
        }
        let cancelled = Arc::new(AtomicBool::new(false));
        self.general_cancel
            .lock()
            .map_err(|_| io::Error::other("general task mutex poisoned"))?
            .insert(task.task_id, Arc::clone(&cancelled));
        let active = Arc::clone(&self.active_tasks);
        let completed = Arc::clone(&self.completed_tasks);
        let failed = Arc::clone(&self.failed_tasks);
        let gate = Arc::clone(&self.resource_gate);
        let cancel_map = Arc::clone(&self.general_cancel);
        let thread = thread::Builder::new()
            .name(format!("nodren-general-{}", task.task_id))
            .spawn(move || {
                active.fetch_add(1, Ordering::AcqRel);
                let _permit = match gate.acquire_requirements(
                    task.spec.cpu_cores,
                    task.spec.ram_gb,
                    task.spec.vram_gb,
                ) {
                    Some(permit) => permit,
                    None => {
                        let result = crate::protocol::GeneralTaskResult {
                            task_id: task.task_id,
                            job_id: task.job_id.clone(),
                            attempt: task.attempt,
                            status: "FAILED".to_string(),
                            exit_code: None,
                            stdout: Vec::new(),
                            stderr: Vec::new(),
                            stdout_truncated: false,
                            stderr_truncated: false,
                            duration_us: 0,
                            error_code: "resource_requirements".to_string(),
                            error: "worker resource gate could not reserve the requested resources"
                                .to_string(),
                        };
                        let write_result = encode_general_task_result(&result)
                            .map_err(|_| io::Error::other("general task result encoding failed"))
                            .and_then(|payload| {
                                writer
                                    .lock()
                                    .map_err(|_| io::Error::other("worker writer mutex poisoned"))
                                    .and_then(|mut writer| {
                                        write_frame(
                                            &mut *writer,
                                            MessageType::TaskResult,
                                            request_id,
                                            &payload,
                                        )
                                    })
                            });
                        if write_result.is_err() {
                            connection_failed.store(true, Ordering::Release);
                        }
                        failed.fetch_add(1, Ordering::AcqRel);
                        active.fetch_sub(1, Ordering::AcqRel);
                        if let Ok(mut map) = cancel_map.lock() {
                            map.remove(&task.task_id);
                        }
                        return;
                    }
                };
                let output_stream = if task.spec.workload_kind == "ai-llama.cpp" {
                    let stream_writer = Arc::clone(&writer);
                    let stream_failed = Arc::clone(&connection_failed);
                    Some(Arc::new(move |chunk: &[u8]| {
                        let Ok(payload) = encode_task_output(task.task_id, "stdout", false, chunk)
                        else {
                            stream_failed.store(true, Ordering::Release);
                            return;
                        };
                        let write_result = stream_writer
                            .lock()
                            .map_err(|_| io::Error::other("worker writer mutex poisoned"))
                            .and_then(|mut stream| {
                                write_frame(
                                    &mut *stream,
                                    MessageType::TaskState,
                                    task.task_id,
                                    &payload,
                                )
                            });
                        if write_result.is_err() {
                            stream_failed.store(true, Ordering::Release);
                        }
                    }) as Arc<dyn Fn(&[u8]) + Send + Sync>)
                } else {
                    None
                };
                let execution = process_executor::execute(
                    task.clone(),
                    cancelled,
                    input_artifacts,
                    output_stream,
                );
                let mut result = execution.result;
                let mut transfer_error = None;
                if result.status == "COMPLETED" {
                    for artifact in &execution.output_artifacts {
                        if let Err(error) = send_output_artifact(&writer, task.task_id, artifact) {
                            transfer_error = Some(error.to_string());
                            break;
                        }
                    }
                }
                if let Some(error) = transfer_error {
                    result.status = "FAILED".to_string();
                    result.exit_code = None;
                    result.error_code = "output_transfer_failed".to_string();
                    result.error = error;
                }
                if let Some(path) = execution.cleanup_directory {
                    let _ = fs::remove_dir_all(path);
                }
                record_task_outcome(&result.status, &completed, &failed);
                let write_result = encode_general_task_result(&result)
                    .map_err(|_| io::Error::other("general task result encoding failed"))
                    .and_then(|payload| {
                        writer
                            .lock()
                            .map_err(|_| io::Error::other("worker writer mutex poisoned"))
                            .and_then(|mut writer| {
                                write_frame(
                                    &mut *writer,
                                    MessageType::TaskResult,
                                    request_id,
                                    &payload,
                                )
                            })
                    });
                if write_result.is_err() {
                    connection_failed.store(true, Ordering::Release);
                }
                active.fetch_sub(1, Ordering::AcqRel);
                if let Ok(mut map) = cancel_map.lock() {
                    map.remove(&result.task_id);
                }
            })
            .map_err(|error| io::Error::other(error.to_string()))?;
        self.general_threads
            .lock()
            .map_err(|_| io::Error::other("general thread mutex poisoned"))?
            .push(thread);
        Ok(())
    }

    pub fn begin_artifact(&self, begin: ArtifactBegin) -> io::Result<()> {
        if begin.artifact.size > MAX_ARTIFACT_SIZE {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "artifact exceeds the worker size limit",
            ));
        }
        let directory = std::env::temp_dir().join("nodren-worker-artifacts");
        fs::create_dir_all(&directory)?;
        let safe_id: String = begin
            .artifact
            .id
            .chars()
            .map(|value| {
                if value.is_ascii_alphanumeric() {
                    value
                } else {
                    '_'
                }
            })
            .collect();
        let path = directory.join(format!(
            "{}-{}-{}.part",
            begin.task_id,
            safe_id,
            std::process::id()
        ));
        let key = (begin.task_id, begin.artifact.id.clone());
        let mut pending = self
            .artifact_pending
            .lock()
            .map_err(|_| io::Error::other("artifact mutex poisoned"))?;
        if let Some(previous) = pending.remove(&key) {
            let _ = fs::remove_file(previous.path);
        }
        self.artifact_ready
            .lock()
            .map_err(|_| io::Error::other("artifact mutex poisoned"))?
            .remove(&key)
            .map(|previous| {
                let _ = fs::remove_file(previous.path);
            });
        let _ = File::create(&path)?;
        pending.insert(
            key,
            StoredArtifact {
                spec: begin.artifact,
                path,
                next_offset: 0,
            },
        );
        Ok(())
    }

    pub fn append_artifact(&self, chunk: ArtifactChunk) -> io::Result<()> {
        let key = (chunk.task_id, chunk.artifact_id.clone());
        let mut pending = self
            .artifact_pending
            .lock()
            .map_err(|_| io::Error::other("artifact mutex poisoned"))?;
        let stored = pending
            .get_mut(&key)
            .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "artifact was not started"))?;
        let end = stored
            .next_offset
            .checked_add(chunk.data.len() as u64)
            .ok_or_else(|| {
                io::Error::new(io::ErrorKind::InvalidData, "artifact chunk offset overflow")
            })?;
        if chunk.offset != stored.next_offset || end > stored.spec.size {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "invalid artifact chunk offset or size",
            ));
        }
        let mut file = OpenOptions::new().append(true).open(&stored.path)?;
        file.seek(SeekFrom::End(0))?;
        if let Err(error) = file.write_all(&chunk.data) {
            let path = stored.path.clone();
            drop(file);
            if let Some(removed) = pending.remove(&key) {
                let _ = fs::remove_file(removed.path);
            } else {
                let _ = fs::remove_file(path);
            }
            return Err(error);
        }
        stored.next_offset += chunk.data.len() as u64;
        Ok(())
    }

    pub fn finish_artifact(&self, task_id: u64, artifact_id: String) -> io::Result<()> {
        let key = (task_id, artifact_id);
        let stored = self
            .artifact_pending
            .lock()
            .map_err(|_| io::Error::other("artifact mutex poisoned"))?
            .remove(&key)
            .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "artifact was not started"))?;
        if stored.next_offset != stored.spec.size {
            let _ = fs::remove_file(&stored.path);
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "artifact size mismatch",
            ));
        }
        if !stored.spec.sha256.is_empty() {
            let actual = match sha256_file(&stored.path) {
                Ok(actual) => actual,
                Err(error) => {
                    let _ = fs::remove_file(&stored.path);
                    return Err(error);
                }
            };
            if !actual.eq_ignore_ascii_case(&stored.spec.sha256) {
                let _ = fs::remove_file(&stored.path);
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    "artifact checksum mismatch",
                ));
            }
        }
        let mut ready = self
            .artifact_ready
            .lock()
            .map_err(|_| io::Error::other("artifact mutex poisoned"))?;
        if let Some(previous) = ready.insert(key, stored) {
            let _ = fs::remove_file(previous.path);
        }
        Ok(())
    }

    fn take_artifact(&self, task_id: u64, artifact_id: &str) -> Option<StoredArtifact> {
        self.artifact_ready
            .lock()
            .ok()?
            .remove(&(task_id, artifact_id.to_string()))
    }

    pub fn cancel_general(&self, task_id: u64) -> bool {
        self.general_cancel
            .lock()
            .ok()
            .and_then(|map| map.get(&task_id).cloned())
            .map(|flag| {
                flag.store(true, Ordering::Release);
                true
            })
            .unwrap_or(false)
    }
}

fn send_output_artifact(
    writer: &Arc<Mutex<TcpStream>>,
    task_id: u64,
    artifact: &process_executor::ProducedArtifact,
) -> io::Result<()> {
    let begin = encode_artifact_begin(task_id, &artifact.spec)?;
    writer
        .lock()
        .map_err(|_| io::Error::other("worker writer mutex poisoned"))
        .and_then(|mut stream| {
            write_frame(&mut *stream, MessageType::ArtifactBegin, task_id, &begin)
        })?;
    let mut file = File::open(&artifact.path)?;
    let mut buffer = vec![0u8; 64 << 10];
    let mut offset = 0u64;
    loop {
        let count = file.read(&mut buffer)?;
        if count == 0 {
            break;
        }
        let chunk = encode_artifact_chunk(task_id, &artifact.spec.id, offset, &buffer[..count])?;
        writer
            .lock()
            .map_err(|_| io::Error::other("worker writer mutex poisoned"))
            .and_then(|mut stream| {
                write_frame(&mut *stream, MessageType::ArtifactChunk, task_id, &chunk)
            })?;
        offset += count as u64;
    }
    let end = encode_artifact_end(task_id, &artifact.spec.id)?;
    writer
        .lock()
        .map_err(|_| io::Error::other("worker writer mutex poisoned"))
        .and_then(|mut stream| write_frame(&mut *stream, MessageType::ArtifactEnd, task_id, &end))
}

fn record_task_outcome(status: &str, completed: &AtomicU64, failed: &AtomicU64) {
    if status == "COMPLETED" {
        completed.fetch_add(1, Ordering::AcqRel);
    } else {
        failed.fetch_add(1, Ordering::AcqRel);
    }
}

impl Drop for WorkerExecutor {
    fn drop(&mut self) {
        self.queue.stop();
        for worker in self.workers.drain(..) {
            let _ = worker.join();
        }
        if let Ok(mut threads) = self.general_threads.lock() {
            for thread in threads.drain(..) {
                let _ = thread.join();
            }
        }
        if let Ok(mut pending) = self.artifact_pending.lock() {
            for (_, artifact) in pending.drain() {
                let _ = fs::remove_file(artifact.path);
            }
        }
        if let Ok(mut ready) = self.artifact_ready.lock() {
            for (_, artifact) in ready.drain() {
                let _ = fs::remove_file(artifact.path);
            }
        }
    }
}

fn has_value(values: &[String], target: &str) -> bool {
    values
        .iter()
        .any(|value| value.eq_ignore_ascii_case(target))
}

fn validate_general_task(node: &NodeInfo, task: &GeneralTaskEnvelope) -> io::Result<()> {
    let spec = &task.spec;
    if !spec.strategy.is_empty() && spec.strategy != "SINGLE" && spec.strategy != "BATCH" {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "execution strategy requires a workload adapter",
        ));
    }
    if spec.required_workers > 1 || spec.replicas > 1 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "multi-worker execution requires a workload adapter",
        ));
    }
    if spec.cpu_cores.max(1) > node.cpu_cores || spec.ram_gb > node.ram_gb {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "resource requirements exceed worker capacity",
        ));
    }
    let available_gpu_count = if node.gpu.count == 0 && !node.gpu.model.is_empty() {
        1
    } else {
        node.gpu.count
    };
    let required_gpu_count = if spec.gpu_count > 0 {
        spec.gpu_count
    } else if spec.gpu_required {
        1
    } else {
        0
    };
    if required_gpu_count > available_gpu_count
        || spec.vram_gb > node.gpu.vram_gb
        || (spec.gpu_required && node.gpu.model.is_empty() && available_gpu_count == 0)
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "GPU requirements are not satisfied",
        ));
    }
    if !spec.accelerator_type.is_empty()
        && !node.gpu.vendor.eq_ignore_ascii_case(&spec.accelerator_type)
        && !node
            .gpu
            .runtime
            .eq_ignore_ascii_case(&spec.accelerator_type)
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "accelerator type is not available",
        ));
    }
    for capability in &spec.gpu_capabilities {
        if !has_value(&node.gpu.capabilities, capability) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "GPU capability is unavailable",
            ));
        }
    }
    if !spec.target_os.is_empty() && !node.os.eq_ignore_ascii_case(&spec.target_os) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "task OS constraint is not satisfied",
        ));
    }
    if !spec.target_arch.is_empty() && !node.arch.eq_ignore_ascii_case(&spec.target_arch) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "task architecture constraint is not satisfied",
        ));
    }
    if !spec.allowed_workers.is_empty() && !has_value(&spec.allowed_workers, &node.id) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "worker is not allowed for this task",
        ));
    }
    for runtime in &spec.required_runtimes {
        if !has_value(&node.runtimes, runtime) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "required runtime is unavailable",
            ));
        }
    }
    if !has_value(&node.execution_types, &spec.task_type) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "task type is not supported by worker",
        ));
    }
    for capability in &spec.required_capabilities {
        if !has_value(&node.capabilities, capability) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "required capability is unavailable",
            ));
        }
    }
    Ok(())
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
                max_ram_gb: 0,
                gpu_required: false,
                gpu_count: 0,
                vram_gb: 0,
                accelerator_type: String::new(),
                gpu_capabilities: Vec::new(),
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
            cpu_model: String::new(),
            cpu_cores: 2,
            logical_cpu_cores: 2,
            physical_cpu_cores: 2,
            ram_gb: 4,
            gpu: crate::protocol::GPUInfo {
                vendor: String::new(),
                model: String::new(),
                vram_gb: 0,
                count: 0,
                capabilities: Vec::new(),
                driver: String::new(),
                runtime: String::new(),
            },
            runtimes: Vec::new(),
            execution_types: Vec::new(),
            capabilities: Vec::new(),
        }));

        let first = gate.acquire(&task(1, 1)).expect("first permit");
        let second = gate.acquire(&task(1, 1)).expect("second permit");
        assert!(gate.acquire(&task(3, 1)).is_none());
        drop(first);
        drop(second);
        assert!(gate.acquire(&task(2, 4)).is_some());
    }
}
