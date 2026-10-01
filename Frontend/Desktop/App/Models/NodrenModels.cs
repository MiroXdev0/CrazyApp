using System.Text.Json.Serialization;

namespace App.Models;

public sealed class HealthResponse
{
    public string Status { get; set; } = string.Empty;
    public string Service { get; set; } = string.Empty;
    public string Version { get; set; } = string.Empty;
    public int Nodes { get; set; }
}

public sealed class GpuInfo
{
    public string Vendor { get; set; } = string.Empty;
    public string Model { get; set; } = string.Empty;
    [JsonPropertyName("vram_gb")]
    public ulong VramGb { get; set; }
    public uint Count { get; set; }
    public List<string> Capabilities { get; set; } = [];
    public string Driver { get; set; } = string.Empty;
    public string Runtime { get; set; } = string.Empty;
}

public sealed class NodeInfo
{
    public string Id { get; set; } = string.Empty;
    public string Hostname { get; set; } = string.Empty;
    public string Os { get; set; } = string.Empty;
    public string Arch { get; set; } = string.Empty;
    [JsonPropertyName("cpu_model")]
    public string CpuModel { get; set; } = string.Empty;
    [JsonPropertyName("cpu_cores")]
    public uint CpuCores { get; set; }
    [JsonPropertyName("ram_gb")]
    public ulong RamGb { get; set; }
    public GpuInfo Gpu { get; set; } = new();
}

public sealed class NodeRecord
{
    public NodeInfo Info { get; set; } = new();
    public string State { get; set; } = string.Empty;
    [JsonPropertyName("assigned_jobs")]
    public List<string> AssignedJobs { get; set; } = [];
    [JsonPropertyName("allocated_cpu_cores")]
    public uint AllocatedCpuCores { get; set; }
    [JsonPropertyName("allocated_ram_gb")]
    public ulong AllocatedRamGb { get; set; }
    [JsonPropertyName("allocated_gpu_count")]
    public uint AllocatedGpuCount { get; set; }
    [JsonPropertyName("allocated_vram_gb")]
    public ulong AllocatedVramGb { get; set; }
    [JsonPropertyName("available_cpu_cores")]
    public uint AvailableCpuCores { get; set; }
    [JsonPropertyName("available_ram_gb")]
    public ulong AvailableRamGb { get; set; }
    [JsonPropertyName("capacity_score")]
    public double CapacityScore { get; set; }
    [JsonPropertyName("effective_capacity")]
    public double EffectiveCapacity { get; set; }
    public WorkerTelemetry Telemetry { get; set; } = new();
    [JsonPropertyName("completed_tasks")]
    public ulong CompletedTasks { get; set; }
    [JsonPropertyName("failed_tasks")]
    public ulong FailedTasks { get; set; }
    [JsonPropertyName("total_execution_us")]
    public ulong TotalExecutionUs { get; set; }
    [JsonPropertyName("observed_throughput_units_per_second")]
    public double ObservedThroughputUnitsPerSecond { get; set; }
    [JsonPropertyName("performance_factor")]
    public double PerformanceFactor { get; set; }
    [JsonPropertyName("last_heartbeat")]
    public DateTimeOffset LastHeartbeat { get; set; }
    [JsonPropertyName("connected_at")]
    public DateTimeOffset ConnectedAt { get; set; }
}

public sealed class WorkerTelemetry
{
    public DateTimeOffset Timestamp { get; set; }
    [JsonPropertyName("uptime_seconds")]
    public ulong UptimeSeconds { get; set; }
    [JsonPropertyName("active_tasks")]
    public uint ActiveTasks { get; set; }
    [JsonPropertyName("cpu_utilization_percent")]
    public double CpuUtilizationPercent { get; set; }
    [JsonPropertyName("memory_available_gb")]
    public ulong MemoryAvailableGb { get; set; }
    [JsonPropertyName("memory_utilization_percent")]
    public double MemoryUtilizationPercent { get; set; }
}

public sealed class ResourceRequirements
{
    [JsonPropertyName("cpu_cores")]
    public uint CpuCores { get; set; }
    [JsonPropertyName("ram_gb")]
    public ulong RamGb { get; set; }
    [JsonPropertyName("max_ram_gb")]
    public ulong MaxRamGb { get; set; }
    [JsonPropertyName("gpu_required")]
    public bool GpuRequired { get; set; }
    [JsonPropertyName("gpu_count")]
    public uint GpuCount { get; set; }
    [JsonPropertyName("vram_gb")]
    public ulong VramGb { get; set; }
    [JsonPropertyName("accelerator_type")]
    public string AcceleratorType { get; set; } = string.Empty;
    [JsonPropertyName("gpu_capabilities")]
    public List<string> GpuCapabilities { get; set; } = [];
}

public sealed class JobResult
{
    [JsonPropertyName("task_id")]
    public ulong TaskId { get; set; }
    [JsonPropertyName("job_id")]
    public string JobId { get; set; } = string.Empty;
    public string Status { get; set; } = string.Empty;
    public long Value { get; set; }
    [JsonPropertyName("error_code")]
    public string ErrorCode { get; set; } = string.Empty;
    public string Error { get; set; } = string.Empty;
    [JsonPropertyName("duration_us")]
    public ulong DurationUs { get; set; }
    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = string.Empty;
}

public sealed class DistributionInfo
{
    public string Mode { get; set; } = "automatic";
    public bool Partitionable { get; set; }
    [JsonPropertyName("total_partitions")]
    public int TotalPartitions { get; set; }
    [JsonPropertyName("completed_partitions")]
    public int CompletedPartitions { get; set; }
    [JsonPropertyName("running_partitions")]
    public int RunningPartitions { get; set; }
    [JsonPropertyName("pending_partitions")]
    public int PendingPartitions { get; set; }
    [JsonPropertyName("failed_partitions")]
    public int FailedPartitions { get; set; }
    [JsonPropertyName("requeued_partitions")]
    public int RequeuedPartitions { get; set; }
    [JsonPropertyName("total_units")]
    public ulong TotalUnits { get; set; }
    [JsonPropertyName("completed_units")]
    public ulong CompletedUnits { get; set; }
    [JsonPropertyName("progress_percent")]
    public double ProgressPercent { get; set; }
    [JsonPropertyName("manual_allocations")]
    public Dictionary<string, byte> ManualAllocations { get; set; } = [];
}

public sealed class Partition
{
    public string Id { get; set; } = string.Empty;
    public int Index { get; set; }
    public ulong Units { get; set; }
    public string State { get; set; } = string.Empty;
    [JsonPropertyName("task_id")]
    public ulong TaskId { get; set; }
    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = string.Empty;
    public uint Attempt { get; set; }
    public JobResult? Result { get; set; }
    [JsonPropertyName("execution_duration_us")]
    public ulong ExecutionDurationUs { get; set; }
    [JsonPropertyName("assignment_reason")]
    public string AssignmentReason { get; set; } = string.Empty;
}

public sealed class Job
{
    public string Id { get; set; } = string.Empty;
    public string Command { get; set; } = string.Empty;
    public byte Priority { get; set; }
    public ResourceRequirements Requirements { get; set; } = new();
    public string PayloadBase64 { get; set; } = string.Empty;
    public string Status { get; set; } = string.Empty;
    [JsonPropertyName("node_id")]
    public string NodeId { get; set; } = string.Empty;
    [JsonPropertyName("node_ids")]
    public List<string> NodeIds { get; set; } = [];
    public JobResult? Result { get; set; }
    public DistributionInfo Distribution { get; set; } = new();
    public List<Partition> Partitions { get; set; } = [];
    [JsonPropertyName("created_at")]
    public DateTimeOffset CreatedAt { get; set; }
    [JsonPropertyName("updated_at")]
    public DateTimeOffset UpdatedAt { get; set; }
    [JsonPropertyName("started_at")]
    public DateTimeOffset? StartedAt { get; set; }
    [JsonPropertyName("completed_at")]
    public DateTimeOffset? CompletedAt { get; set; }
    [JsonPropertyName("queue_time_ms")]
    public long QueueTimeMs { get; set; }
    [JsonPropertyName("elapsed_ms")]
    public long ElapsedMs { get; set; }
    public GeneralTaskSpec? Task { get; set; }
    [JsonPropertyName("execution_result")]
    public GeneralTaskResult? Execution { get; set; }
    [JsonPropertyName("batch_id")]
    public string BatchId { get; set; } = string.Empty;
}

public sealed class GeneralTaskSpec
{
    public string Type { get; set; } = string.Empty;
    public string Version { get; set; } = "1";
    public string Executable { get; set; } = string.Empty;
    public string Runtime { get; set; } = string.Empty;
    public string Script { get; set; } = string.Empty;
    public string Workload { get; set; } = string.Empty;
    public List<string> Arguments { get; set; } = [];
    public Dictionary<string, string> Environment { get; set; } = [];
    [JsonPropertyName("working_directory")]
    public string WorkingDirectory { get; set; } = string.Empty;
    [JsonPropertyName("stdin_base64")]
    public string StdinBase64 { get; set; } = string.Empty;
    [JsonPropertyName("input_artifacts")]
    public List<TaskArtifact> InputArtifacts { get; set; } = [];
    [JsonPropertyName("output_artifacts")]
    public List<TaskArtifact> OutputArtifacts { get; set; } = [];
    [JsonPropertyName("timeout_ms")]
    public ulong TimeoutMs { get; set; }
    [JsonPropertyName("stdout_limit_bytes")]
    public ulong StdoutLimitBytes { get; set; }
    [JsonPropertyName("stderr_limit_bytes")]
    public ulong StderrLimitBytes { get; set; }
    public ResourceRequirements Requirements { get; set; } = new();
    public TaskTarget Target { get; set; } = new();
    public RetryPolicy Retry { get; set; } = new();
    [JsonPropertyName("workload_kind")]
    public string WorkloadKind { get; set; } = string.Empty;
    public string Strategy { get; set; } = "SINGLE";
    [JsonPropertyName("required_workers")]
    public uint RequiredWorkers { get; set; }
    public uint Replicas { get; set; }
    [JsonPropertyName("package_manifest")]
    public TaskPackageManifest? PackageManifest { get; set; }
}

public sealed class TaskArtifact
{
    public string Id { get; set; } = string.Empty;
    public string Name { get; set; } = string.Empty;
    public ulong Size { get; set; }
    public string Sha256 { get; set; } = string.Empty;
    public string Kind { get; set; } = string.Empty;
}

public sealed class TaskPackageManifest
{
    [JsonPropertyName("entry_point")]
    public string EntryPoint { get; set; } = string.Empty;
    public string Runtime { get; set; } = string.Empty;
    public List<string> Arguments { get; set; } = [];
    public Dictionary<string, string> Environment { get; set; } = [];
    public string Os { get; set; } = string.Empty;
    public string Arch { get; set; } = string.Empty;
    [JsonPropertyName("required_runtimes")]
    public List<string> RequiredRuntimes { get; set; } = [];
}

public sealed class TaskTarget
{
    public string Os { get; set; } = string.Empty;
    public string Arch { get; set; } = string.Empty;
    [JsonPropertyName("required_runtimes")]
    public List<string> RequiredRuntimes { get; set; } = [];
    [JsonPropertyName("required_capabilities")]
    public List<string> RequiredCapabilities { get; set; } = [];
    [JsonPropertyName("allowed_worker_ids")]
    public List<string> AllowedWorkerIds { get; set; } = [];
    [JsonPropertyName("preferred_worker_id")]
    public string PreferredWorkerId { get; set; } = string.Empty;
}

public sealed class RetryPolicy
{
    [JsonPropertyName("max_retries")]
    public uint MaxRetries { get; set; }
}

public sealed class GeneralTaskResult
{
    public string Status { get; set; } = string.Empty;
    [JsonPropertyName("exit_code")]
    public int? ExitCode { get; set; }
    [JsonPropertyName("stdout_base64")]
    public string StdoutBase64 { get; set; } = string.Empty;
    [JsonPropertyName("stderr_base64")]
    public string StderrBase64 { get; set; } = string.Empty;
    [JsonPropertyName("stdout_truncated")]
    public bool StdoutTruncated { get; set; }
    [JsonPropertyName("stderr_truncated")]
    public bool StderrTruncated { get; set; }
    [JsonPropertyName("duration_us")]
    public ulong DurationUs { get; set; }
    [JsonPropertyName("error_code")]
    public string ErrorCode { get; set; } = string.Empty;
    public string Error { get; set; } = string.Empty;
    public uint Attempt { get; set; }
}

public sealed class TaskRequest
{
    [JsonPropertyName("id")]
    public string Id { get; set; } = string.Empty;
    [JsonPropertyName("batch_id")]
    public string BatchId { get; set; } = string.Empty;
    public GeneralTaskSpec Task { get; set; } = new();
}

public sealed class JobRequest
{
    public string Command { get; set; } = string.Empty;
    public byte Priority { get; set; }
    public ResourceRequirements Requirements { get; set; } = new();
    [JsonPropertyName("payload_base64")]
    public string PayloadBase64 { get; set; } = string.Empty;
    [JsonPropertyName("distribution_mode")]
    public string DistributionMode { get; set; } = "automatic";
    [JsonPropertyName("manual_allocations")]
    public Dictionary<string, byte>? ManualAllocations { get; set; }
    public bool? Partitionable { get; set; }
}

public sealed class DistributionUpdateRequest
{
    public string Mode { get; set; } = "automatic";
    [JsonPropertyName("manual_allocations")]
    public Dictionary<string, byte>? ManualAllocations { get; set; }
}
