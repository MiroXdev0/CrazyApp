use std::io::{self, Read, Write};
use std::net::TcpStream;

// Little-endian encoding of the ASCII bytes "NDRN".
pub const MAGIC: u32 = 0x4E52444E;
pub const VERSION: u16 = 1;
pub const MAX_FRAME: usize = 16 * 1024 * 1024;
pub const MAX_BATCH: u32 = 4096;
pub const MAX_STRING: usize = 1024 * 1024;

#[repr(u16)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MessageType {
    Hello = 1,
    Register = 2,
    RegisterAck = 3,
    Heartbeat = 4,
    HeartbeatAck = 5,
    TaskBatch = 6,
    TaskResultBatch = 7,
    Error = 8,
    Goodbye = 9,
    Ready = 10,
    TaskSubmit = 11,
    TaskAck = 12,
    TaskCancel = 13,
    TaskState = 14,
    TaskResult = 15,
    ArtifactBegin = 16,
    ArtifactChunk = 17,
    ArtifactEnd = 18,
    Capabilities = 19,
    AuthChallenge = 20,
    AuthResponse = 21,
    AuthResult = 22,
}

impl TryFrom<u16> for MessageType {
    type Error = io::Error;

    fn try_from(value: u16) -> Result<Self, io::Error> {
        match value {
            1 => Ok(Self::Hello),
            2 => Ok(Self::Register),
            3 => Ok(Self::RegisterAck),
            4 => Ok(Self::Heartbeat),
            5 => Ok(Self::HeartbeatAck),
            6 => Ok(Self::TaskBatch),
            7 => Ok(Self::TaskResultBatch),
            8 => Ok(Self::Error),
            9 => Ok(Self::Goodbye),
            10 => Ok(Self::Ready),
            11 => Ok(Self::TaskSubmit),
            12 => Ok(Self::TaskAck),
            13 => Ok(Self::TaskCancel),
            14 => Ok(Self::TaskState),
            15 => Ok(Self::TaskResult),
            16 => Ok(Self::ArtifactBegin),
            17 => Ok(Self::ArtifactChunk),
            18 => Ok(Self::ArtifactEnd),
            19 => Ok(Self::Capabilities),
            20 => Ok(Self::AuthChallenge),
            21 => Ok(Self::AuthResponse),
            22 => Ok(Self::AuthResult),
            _ => Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "unknown message type",
            )),
        }
    }
}

pub struct Frame {
    pub typ: MessageType,
    pub request_id: u64,
    pub payload: Vec<u8>,
}

pub struct AuthenticatedWriter {
    stream: TcpStream,
    key: Option<[u8; 32]>,
    sequence: u64,
}

impl AuthenticatedWriter {
    pub fn new(stream: TcpStream, key: Option<[u8; 32]>) -> Self {
        Self {
            stream,
            key,
            sequence: 0,
        }
    }

    pub fn write_frame(
        &mut self,
        typ: MessageType,
        request_id: u64,
        payload: &[u8],
    ) -> io::Result<()> {
        if let Some(key) = self.key.as_ref() {
            let sequence = self.sequence.checked_add(1).ok_or_else(|| {
                io::Error::new(
                    io::ErrorKind::InvalidData,
                    "authenticated frame sequence exhausted",
                )
            })?;
            let protected =
                crate::auth::protect_frame(key, b'W', typ as u16, request_id, sequence, payload)?;
            write_frame(&mut self.stream, typ, request_id, &protected)?;
            self.sequence = sequence;
            Ok(())
        } else {
            write_frame(&mut self.stream, typ, request_id, payload)
        }
    }
}

fn put_u32(out: &mut Vec<u8>, v: u32) {
    out.extend_from_slice(&v.to_le_bytes());
}

fn put_u64(out: &mut Vec<u8>, v: u64) {
    out.extend_from_slice(&v.to_le_bytes());
}

fn get_u32(data: &[u8], cursor: &mut usize) -> io::Result<u32> {
    if *cursor + 4 > data.len() {
        return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "u32"));
    }
    let v = u32::from_le_bytes(data[*cursor..*cursor + 4].try_into().unwrap());
    *cursor += 4;
    Ok(v)
}

fn get_u64(data: &[u8], cursor: &mut usize) -> io::Result<u64> {
    if *cursor + 8 > data.len() {
        return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "u64"));
    }
    let v = u64::from_le_bytes(data[*cursor..*cursor + 8].try_into().unwrap());
    *cursor += 8;
    Ok(v)
}

pub fn put_string(out: &mut Vec<u8>, value: &str) -> io::Result<()> {
    if value.len() > MAX_STRING {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "string too large",
        ));
    }
    put_u32(out, value.len() as u32);
    out.extend_from_slice(value.as_bytes());
    Ok(())
}

fn put_string_array(out: &mut Vec<u8>, values: &[String]) -> io::Result<()> {
    if values.len() > MAX_BATCH as usize {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "array too large",
        ));
    }
    put_u32(out, values.len() as u32);
    for value in values {
        put_string(out, value)?;
    }
    Ok(())
}

fn get_string_array(data: &[u8], cursor: &mut usize) -> io::Result<Vec<String>> {
    let count = get_u32(data, cursor)?;
    if count > MAX_BATCH {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "array too large",
        ));
    }
    let mut values = Vec::with_capacity(count as usize);
    for _ in 0..count {
        values.push(get_string(data, cursor)?);
    }
    Ok(values)
}

fn put_bytes(out: &mut Vec<u8>, value: &[u8]) -> io::Result<()> {
    if value.len() > MAX_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "bytes too large",
        ));
    }
    put_u32(out, value.len() as u32);
    out.extend_from_slice(value);
    Ok(())
}

fn get_bytes(data: &[u8], cursor: &mut usize) -> io::Result<Vec<u8>> {
    let length = get_u32(data, cursor)? as usize;
    if length > MAX_FRAME || *cursor + length > data.len() {
        return Err(io::Error::new(io::ErrorKind::InvalidData, "invalid bytes"));
    }
    let value = data[*cursor..*cursor + length].to_vec();
    *cursor += length;
    Ok(value)
}

pub fn get_string(data: &[u8], cursor: &mut usize) -> io::Result<String> {
    let len = get_u32(data, cursor)? as usize;
    if len > MAX_STRING || *cursor + len > data.len() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid string length",
        ));
    }
    let value = String::from_utf8(data[*cursor..*cursor + len].to_vec())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid utf-8"))?;
    *cursor += len;
    Ok(value)
}

pub fn write_frame<W: Write>(
    writer: &mut W,
    typ: MessageType,
    request_id: u64,
    payload: &[u8],
) -> io::Result<()> {
    if payload.len() > MAX_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "frame too large",
        ));
    }

    writer.write_all(&MAGIC.to_le_bytes())?;
    writer.write_all(&VERSION.to_le_bytes())?;
    writer.write_all(&(typ as u16).to_le_bytes())?;
    writer.write_all(&request_id.to_le_bytes())?;
    writer.write_all(&(payload.len() as u32).to_le_bytes())?;
    writer.write_all(payload)?;
    writer.flush()
}

pub fn read_frame<R: Read>(reader: &mut R) -> io::Result<Frame> {
    let mut header = [0u8; 20];
    reader.read_exact(&mut header)?;

    let magic = u32::from_le_bytes(header[0..4].try_into().unwrap());
    let version = u16::from_le_bytes(header[4..6].try_into().unwrap());
    if magic != MAGIC {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            format!("invalid magic: got {magic:#010x}, expected {MAGIC:#010x}"),
        ));
    }
    if version != VERSION {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "unsupported protocol version",
        ));
    }

    let typ = MessageType::try_from(u16::from_le_bytes(header[6..8].try_into().unwrap()))?;
    let request_id = u64::from_le_bytes(header[8..16].try_into().unwrap());
    let len = u32::from_le_bytes(header[16..20].try_into().unwrap()) as usize;

    if len > MAX_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "frame exceeds limit",
        ));
    }

    let mut payload = vec![0u8; len];
    reader.read_exact(&mut payload)?;
    Ok(Frame {
        typ,
        request_id,
        payload,
    })
}

#[derive(Debug, Clone)]
pub struct ResourceRequirements {
    pub cpu_cores: u32,
    pub ram_gb: u64,
    pub max_ram_gb: u64,
    pub gpu_required: bool,
    pub gpu_count: u32,
    pub vram_gb: u64,
    pub accelerator_type: String,
    pub gpu_capabilities: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct GPUInfo {
    pub vendor: String,
    pub model: String,
    pub vram_gb: u64,
    pub count: u32,
    pub capabilities: Vec<String>,
    pub driver: String,
    pub runtime: String,
}

#[derive(Debug, Clone)]
pub struct NodeInfo {
    pub id: String,
    pub hostname: String,
    pub os: String,
    pub arch: String,
    pub cpu_model: String,
    pub cpu_cores: u32,
    pub logical_cpu_cores: u32,
    pub physical_cpu_cores: u32,
    pub ram_gb: u64,
    pub gpu: GPUInfo,
    pub runtimes: Vec<String>,
    pub execution_types: Vec<String>,
    pub capabilities: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct Task {
    pub id: u64,
    pub job_id: String,
    pub command: String,
    #[allow(dead_code)]
    pub priority: u8,
    pub requirements: ResourceRequirements,
    pub payload: Vec<u8>,
}

#[derive(Debug, Clone)]
pub struct TaskResult {
    pub task_id: u64,
    pub job_id: String,
    pub status: String,
    pub value: i64,
    pub error_code: String,
    pub error: String,
    pub duration_us: u64,
    pub node_id: String,
}

#[derive(Debug, Clone)]
pub struct GeneralTaskSpec {
    pub task_type: String,
    pub version: String,
    pub executable: String,
    pub runtime: String,
    pub script: String,
    pub workload: String,
    pub arguments: Vec<String>,
    pub environment: Vec<(String, String)>,
    pub working_directory: String,
    pub stdin: Vec<u8>,
    pub timeout_ms: u64,
    pub stdout_limit_bytes: u64,
    pub stderr_limit_bytes: u64,
    pub cpu_cores: u32,
    pub ram_gb: u64,
    pub max_ram_gb: u64,
    pub gpu_required: bool,
    pub gpu_count: u32,
    pub vram_gb: u64,
    pub accelerator_type: String,
    pub gpu_capabilities: Vec<String>,
    pub target_os: String,
    pub target_arch: String,
    pub required_runtimes: Vec<String>,
    pub required_capabilities: Vec<String>,
    pub allowed_workers: Vec<String>,
    pub preferred_worker: String,
    pub input_artifacts: Vec<ArtifactSpec>,
    pub output_artifacts: Vec<ArtifactSpec>,
    pub workload_kind: String,
    pub strategy: String,
    pub required_workers: u32,
    pub replicas: u32,
    pub package_manifest: Option<TaskPackageManifest>,
    pub max_retries: u32,
}

#[derive(Debug, Clone)]
pub struct TaskPackageManifest {
    pub entry_point: String,
    pub runtime: String,
    pub arguments: Vec<String>,
    pub environment: Vec<(String, String)>,
    pub os: String,
    pub arch: String,
    pub required_runtimes: Vec<String>,
}

#[derive(Debug, Clone)]
pub struct ArtifactSpec {
    pub id: String,
    pub name: String,
    pub size: u64,
    pub sha256: String,
    pub kind: String,
}

#[derive(Debug, Clone)]
pub struct GeneralTaskEnvelope {
    pub task_id: u64,
    pub job_id: String,
    pub attempt: u32,
    pub spec: GeneralTaskSpec,
}

#[derive(Debug, Clone)]
pub struct GeneralTaskResult {
    pub task_id: u64,
    pub job_id: String,
    pub attempt: u32,
    pub status: String,
    pub exit_code: Option<i32>,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    pub stdout_truncated: bool,
    pub stderr_truncated: bool,
    pub duration_us: u64,
    pub error_code: String,
    pub error: String,
}

pub struct ArtifactBegin {
    pub task_id: u64,
    pub artifact: ArtifactSpec,
}

pub struct ArtifactChunk {
    pub task_id: u64,
    pub artifact_id: String,
    pub offset: u64,
    pub data: Vec<u8>,
}

pub fn encode_artifact_begin(task_id: u64, artifact: &ArtifactSpec) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    put_u64(&mut out, task_id);
    put_string(&mut out, &artifact.id)?;
    put_string(&mut out, &artifact.name)?;
    put_string(&mut out, &artifact.sha256)?;
    put_string(&mut out, &artifact.kind)?;
    put_u64(&mut out, artifact.size);
    Ok(out)
}

pub fn encode_artifact_chunk(
    task_id: u64,
    artifact_id: &str,
    offset: u64,
    data: &[u8],
) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    put_u64(&mut out, task_id);
    put_string(&mut out, artifact_id)?;
    put_u64(&mut out, offset);
    put_bytes(&mut out, data)?;
    Ok(out)
}

pub fn encode_artifact_end(task_id: u64, artifact_id: &str) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    put_u64(&mut out, task_id);
    put_string(&mut out, artifact_id)?;
    Ok(out)
}

pub fn decode_artifact_begin(data: &[u8]) -> io::Result<ArtifactBegin> {
    let mut cursor = 0;
    let task_id = get_u64(data, &mut cursor)?;
    let id = get_string(data, &mut cursor)?;
    let name = get_string(data, &mut cursor)?;
    let sha256 = get_string(data, &mut cursor)?;
    let kind = get_string(data, &mut cursor)?;
    let size = get_u64(data, &mut cursor)?;
    if cursor != data.len() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "artifact begin trailing data",
        ));
    }
    Ok(ArtifactBegin {
        task_id,
        artifact: ArtifactSpec {
            id,
            name,
            size,
            sha256,
            kind,
        },
    })
}

pub fn decode_artifact_chunk(data: &[u8]) -> io::Result<ArtifactChunk> {
    let mut cursor = 0;
    let task_id = get_u64(data, &mut cursor)?;
    let artifact_id = get_string(data, &mut cursor)?;
    let offset = get_u64(data, &mut cursor)?;
    let bytes = get_bytes(data, &mut cursor)?;
    if cursor != data.len() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "artifact chunk trailing data",
        ));
    }
    Ok(ArtifactChunk {
        task_id,
        artifact_id,
        offset,
        data: bytes,
    })
}

pub fn decode_artifact_end(data: &[u8]) -> io::Result<(u64, String)> {
    let mut cursor = 0;
    let task_id = get_u64(data, &mut cursor)?;
    let artifact_id = get_string(data, &mut cursor)?;
    if cursor != data.len() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "artifact end trailing data",
        ));
    }
    Ok((task_id, artifact_id))
}

pub fn encode_register(node: &NodeInfo) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    for s in [
        &node.id,
        &node.hostname,
        &node.os,
        &node.arch,
        &node.gpu.vendor,
        &node.gpu.model,
        &node.cpu_model,
    ] {
        put_string(&mut out, s)?;
    }
    put_string_array(&mut out, &node.runtimes)?;
    put_string_array(&mut out, &node.execution_types)?;
    put_string_array(&mut out, &node.capabilities)?;
    put_u64(&mut out, node.cpu_cores as u64);
    put_u64(&mut out, node.ram_gb);
    put_u64(&mut out, node.gpu.vram_gb);
    put_u64(&mut out, node.gpu.count as u64);
    put_string_array(&mut out, &node.gpu.capabilities)?;
    // Topology fields are appended so older controllers can still decode the
    // original registration payload and its capacity fields.
    put_u64(&mut out, node.logical_cpu_cores as u64);
    put_u64(&mut out, node.physical_cpu_cores as u64);
    Ok(out)
}

pub fn decode_task_batch(data: &[u8]) -> io::Result<Vec<Task>> {
    if data.len() < 4 {
        return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "batch count"));
    }

    let count = u32::from_le_bytes(data[0..4].try_into().unwrap());
    if count == 0 || count > MAX_BATCH {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid task batch size",
        ));
    }

    let mut cursor = 4usize;
    let mut tasks = Vec::with_capacity(count as usize);

    for _ in 0..count {
        let id = get_u64(data, &mut cursor)?;
        let job_id = get_string(data, &mut cursor)?;
        let command = get_string(data, &mut cursor)?;

        if cursor + 1 + 4 + 8 + 1 + 4 > data.len() {
            return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "task header"));
        }

        let priority = data[cursor];
        cursor += 1;

        let cpu_cores = u32::from_le_bytes(data[cursor..cursor + 4].try_into().unwrap());
        cursor += 4;

        let ram_gb = u64::from_le_bytes(data[cursor..cursor + 8].try_into().unwrap());
        cursor += 8;

        let gpu_required = data[cursor] != 0;
        cursor += 1;

        let payload_len = u32::from_le_bytes(data[cursor..cursor + 4].try_into().unwrap()) as usize;
        cursor += 4;
        if payload_len > MAX_FRAME || cursor + payload_len > data.len() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "invalid task payload",
            ));
        }

        tasks.push(Task {
            id,
            job_id,
            command,
            priority,
            requirements: ResourceRequirements {
                cpu_cores,
                ram_gb,
                max_ram_gb: 0,
                gpu_required,
                gpu_count: 0,
                vram_gb: 0,
                accelerator_type: String::new(),
                gpu_capabilities: Vec::new(),
            },
            payload: data[cursor..cursor + payload_len].to_vec(),
        });
        cursor += payload_len;
    }

    Ok(tasks)
}

pub fn encode_task_results(results: &[TaskResult]) -> io::Result<Vec<u8>> {
    if results.is_empty() || results.len() > MAX_BATCH as usize {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid result batch",
        ));
    }

    let mut out = Vec::new();
    put_u32(&mut out, results.len() as u32);

    for result in results {
        put_u64(&mut out, result.task_id);
        put_string(&mut out, &result.job_id)?;
        put_string(&mut out, &result.status)?;
        put_u64(&mut out, result.value as u64);
        put_string(&mut out, &result.error)?;
        put_u64(&mut out, result.duration_us);
        put_string(&mut out, &result.node_id)?;
        put_string(&mut out, &result.error_code)?;
    }

    Ok(out)
}

pub fn decode_general_task(data: &[u8]) -> io::Result<GeneralTaskEnvelope> {
    let mut cursor = 0usize;
    let task_id = get_u64(data, &mut cursor)?;
    let job_id = get_string(data, &mut cursor)?;
    let attempt = get_u32(data, &mut cursor)?;
    let task_type = get_string(data, &mut cursor)?;
    let version = get_string(data, &mut cursor)?;
    let executable = get_string(data, &mut cursor)?;
    let runtime = get_string(data, &mut cursor)?;
    let script = get_string(data, &mut cursor)?;
    let workload = get_string(data, &mut cursor)?;
    let working_directory = get_string(data, &mut cursor)?;
    let arguments = get_string_array(data, &mut cursor)?;
    let environment_values = get_string_array(data, &mut cursor)?;
    if environment_values.len() % 2 != 0 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid environment",
        ));
    }
    let environment = environment_values
        .chunks_exact(2)
        .map(|entry| (entry[0].clone(), entry[1].clone()))
        .collect();
    let stdin = get_bytes(data, &mut cursor)?;
    let timeout_ms = get_u64(data, &mut cursor)?;
    let stdout_limit_bytes = get_u64(data, &mut cursor)?;
    let stderr_limit_bytes = get_u64(data, &mut cursor)?;
    let ram_gb = get_u64(data, &mut cursor)?;
    let max_ram_gb = get_u64(data, &mut cursor)?;
    let cpu_cores = get_u32(data, &mut cursor)?;
    if cursor >= data.len() {
        return Err(io::Error::new(
            io::ErrorKind::UnexpectedEof,
            "gpu requirement",
        ));
    }
    let gpu_required = data[cursor] != 0;
    cursor += 1;
    let gpu_count = get_u32(data, &mut cursor)?;
    let vram_gb = get_u64(data, &mut cursor)?;
    let accelerator_type = get_string(data, &mut cursor)?;
    let gpu_capabilities = get_string_array(data, &mut cursor)?;
    let target_os = get_string(data, &mut cursor)?;
    let target_arch = get_string(data, &mut cursor)?;
    let preferred_worker = get_string(data, &mut cursor)?;
    let required_runtimes = get_string_array(data, &mut cursor)?;
    let required_capabilities = get_string_array(data, &mut cursor)?;
    let allowed_workers = get_string_array(data, &mut cursor)?;
    let input_artifacts = get_artifact_array(data, &mut cursor)?;
    let output_artifacts = get_artifact_array(data, &mut cursor)?;
    let workload_kind = get_string(data, &mut cursor)?;
    let strategy = get_string(data, &mut cursor)?;
    let required_workers = get_u32(data, &mut cursor)?;
    let replicas = get_u32(data, &mut cursor)?;
    if cursor >= data.len() {
        return Err(io::Error::new(
            io::ErrorKind::UnexpectedEof,
            "package manifest flag",
        ));
    }
    let package_manifest = if data[cursor] != 0 {
        cursor += 1;
        let entry_point = get_string(data, &mut cursor)?;
        let runtime = get_string(data, &mut cursor)?;
        let os = get_string(data, &mut cursor)?;
        let arch = get_string(data, &mut cursor)?;
        let arguments = get_string_array(data, &mut cursor)?;
        let required_runtimes = get_string_array(data, &mut cursor)?;
        let environment_values = get_string_array(data, &mut cursor)?;
        if environment_values.len() % 2 != 0 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "invalid package manifest environment",
            ));
        }
        let environment = environment_values
            .chunks_exact(2)
            .map(|entry| (entry[0].clone(), entry[1].clone()))
            .collect();
        Some(TaskPackageManifest {
            entry_point,
            runtime,
            arguments,
            environment,
            os,
            arch,
            required_runtimes,
        })
    } else {
        cursor += 1;
        None
    };
    let max_retries = get_u32(data, &mut cursor)?;
    if cursor != data.len() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "general task trailing data",
        ));
    }
    if task_type.is_empty() || version.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "task type and version are required",
        ));
    }
    Ok(GeneralTaskEnvelope {
        task_id,
        job_id,
        attempt,
        spec: GeneralTaskSpec {
            task_type,
            version,
            executable,
            runtime,
            script,
            workload,
            arguments,
            environment,
            working_directory,
            stdin,
            timeout_ms,
            stdout_limit_bytes,
            stderr_limit_bytes,
            cpu_cores,
            ram_gb,
            max_ram_gb,
            gpu_required,
            gpu_count,
            vram_gb,
            accelerator_type,
            gpu_capabilities,
            target_os,
            target_arch,
            required_runtimes,
            required_capabilities,
            allowed_workers,
            preferred_worker,
            input_artifacts,
            output_artifacts,
            workload_kind,
            strategy,
            required_workers,
            replicas,
            package_manifest,
            max_retries,
        },
    })
}

fn get_artifact_array(data: &[u8], cursor: &mut usize) -> io::Result<Vec<ArtifactSpec>> {
    let count = get_u32(data, cursor)?;
    if count > MAX_BATCH {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "too many artifacts",
        ));
    }
    let mut artifacts = Vec::with_capacity(count as usize);
    for _ in 0..count {
        artifacts.push(ArtifactSpec {
            id: get_string(data, cursor)?,
            name: get_string(data, cursor)?,
            sha256: get_string(data, cursor)?,
            kind: get_string(data, cursor)?,
            size: get_u64(data, cursor)?,
        });
    }
    Ok(artifacts)
}

pub fn encode_general_task_result(result: &GeneralTaskResult) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    put_u64(&mut out, result.task_id);
    put_string(&mut out, &result.job_id)?;
    put_u32(&mut out, result.attempt);
    put_string(&mut out, &result.status)?;
    put_string(&mut out, &result.error_code)?;
    put_string(&mut out, &result.error)?;
    out.push(u8::from(result.exit_code.is_some()));
    if let Some(exit_code) = result.exit_code {
        out.extend_from_slice(&exit_code.to_le_bytes());
    }
    put_bytes(&mut out, &result.stdout)?;
    put_bytes(&mut out, &result.stderr)?;
    out.push(u8::from(result.stdout_truncated));
    out.push(u8::from(result.stderr_truncated));
    put_u64(&mut out, result.duration_us);
    Ok(out)
}

pub fn encode_task_output(
    task_id: u64,
    stream: &str,
    final_chunk: bool,
    data: &[u8],
) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    put_u64(&mut out, task_id);
    put_string(&mut out, stream)?;
    out.push(u8::from(final_chunk));
    put_bytes(&mut out, data)?;
    Ok(out)
}

pub fn encode_register_ack(message: &str) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    put_string(&mut out, message)?;
    Ok(out)
}

pub fn encode_heartbeat(
    unix_ms: i64,
    uptime_seconds: u64,
    active_tasks: u32,
    cpu_percent: Option<f64>,
    memory_available_gb: Option<u64>,
) -> Vec<u8> {
    let mut out = Vec::with_capacity(32);
    out.extend_from_slice(&unix_ms.to_le_bytes());
    out.extend_from_slice(&uptime_seconds.to_le_bytes());
    out.extend_from_slice(&active_tasks.to_le_bytes());
    let cpu_milli = cpu_percent
        .map(|value| (value.clamp(0.0, 100.0) * 1000.0).round() as u32)
        .unwrap_or(u32::MAX);
    out.extend_from_slice(&cpu_milli.to_le_bytes());
    out.extend_from_slice(
        &memory_available_gb
            .map(|value| value * 1024)
            .unwrap_or(u64::MAX)
            .to_le_bytes(),
    );
    out
}

pub fn encode_heartbeat_with_counters(
    unix_ms: i64,
    uptime_seconds: u64,
    active_tasks: u32,
    completed_tasks: u64,
    failed_tasks: u64,
    cpu_percent: Option<f64>,
    memory_available_gb: Option<u64>,
) -> Vec<u8> {
    let mut out = encode_heartbeat(
        unix_ms,
        uptime_seconds,
        active_tasks,
        cpu_percent,
        memory_available_gb,
    );
    out.extend_from_slice(&completed_tasks.to_le_bytes());
    out.extend_from_slice(&failed_tasks.to_le_bytes());
    out
}

pub fn encode_heartbeat_with_counters_and_gpu(
    unix_ms: i64,
    uptime_seconds: u64,
    active_tasks: u32,
    completed_tasks: u64,
    failed_tasks: u64,
    cpu_percent: Option<f64>,
    memory_available_gb: Option<u64>,
    gpu_available_vram_gb: Option<u64>,
    gpu_utilization_percent: Option<f64>,
) -> Vec<u8> {
    let mut out = encode_heartbeat(
        unix_ms,
        uptime_seconds,
        active_tasks,
        cpu_percent,
        memory_available_gb,
    );
    out.extend_from_slice(&completed_tasks.to_le_bytes());
    out.extend_from_slice(&failed_tasks.to_le_bytes());
    out.extend_from_slice(
        &gpu_available_vram_gb
            .map(|value| value * 1024)
            .unwrap_or(u64::MAX)
            .to_le_bytes(),
    );
    let gpu_util_milli = gpu_utilization_percent
        .map(|value| (value.clamp(0.0, 100.0) * 1000.0).round() as u32)
        .unwrap_or(u32::MAX);
    out.extend_from_slice(&gpu_util_milli.to_le_bytes());
    out.extend_from_slice(&0u32.to_le_bytes());
    out
}

#[cfg(test)]
mod heartbeat_tests {
    use super::{
        encode_heartbeat, encode_heartbeat_with_counters, encode_heartbeat_with_counters_and_gpu,
    };

    #[test]
    fn heartbeat_payloads_remain_backward_conscious() {
        assert_eq!(encode_heartbeat(1, 2, 3, Some(4.5), Some(6)).len(), 32);
        let payload = encode_heartbeat_with_counters(1, 2, 3, 11, 7, Some(4.5), Some(6));
        assert_eq!(payload.len(), 48);
        assert_eq!(u64::from_le_bytes(payload[32..40].try_into().unwrap()), 11);
        assert_eq!(u64::from_le_bytes(payload[40..48].try_into().unwrap()), 7);
        let gpu = encode_heartbeat_with_counters_and_gpu(
            1,
            2,
            3,
            11,
            7,
            Some(4.5),
            Some(6),
            Some(12),
            Some(37.5),
        );
        assert_eq!(gpu.len(), 64);
        assert_eq!(
            u64::from_le_bytes(gpu[48..56].try_into().unwrap()),
            12 * 1024
        );
    }
}
