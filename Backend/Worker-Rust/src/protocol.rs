use std::io::{self, Read, Write};

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
    pub gpu_required: bool,
}

#[derive(Debug, Clone)]
pub struct GPUInfo {
    pub vendor: String,
    pub model: String,
    pub vram_gb: u64,
}

#[derive(Debug, Clone)]
pub struct NodeInfo {
    pub id: String,
    pub hostname: String,
    pub os: String,
    pub arch: String,
    pub cpu_cores: u32,
    pub ram_gb: u64,
    pub gpu: GPUInfo,
}

#[derive(Debug, Clone)]
pub struct Task {
    pub id: u64,
    pub job_id: String,
    pub command: String,
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

pub fn encode_register(node: &NodeInfo) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    for s in [
        &node.id,
        &node.hostname,
        &node.os,
        &node.arch,
        &node.gpu.vendor,
        &node.gpu.model,
    ] {
        put_string(&mut out, s)?;
    }
    put_u64(&mut out, node.cpu_cores as u64);
    put_u64(&mut out, node.ram_gb);
    put_u64(&mut out, node.gpu.vram_gb);
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
                gpu_required,
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

pub fn encode_register_ack(message: &str) -> io::Result<Vec<u8>> {
    let mut out = Vec::new();
    put_string(&mut out, message)?;
    Ok(out)
}

pub fn encode_heartbeat(unix_ms: i64) -> Vec<u8> {
    unix_ms.to_le_bytes().to_vec()
}
