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
}

public sealed class NodeInfo
{
    public string Id { get; set; } = string.Empty;
    public string Hostname { get; set; } = string.Empty;
    public string Os { get; set; } = string.Empty;
    public string Arch { get; set; } = string.Empty;
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
    [JsonPropertyName("available_cpu_cores")]
    public uint AvailableCpuCores { get; set; }
    [JsonPropertyName("available_ram_gb")]
    public ulong AvailableRamGb { get; set; }
    [JsonPropertyName("capacity_score")]
    public double CapacityScore { get; set; }
    [JsonPropertyName("effective_capacity")]
    public double EffectiveCapacity { get; set; }
    [JsonPropertyName("last_heartbeat")]
    public DateTimeOffset LastHeartbeat { get; set; }
    [JsonPropertyName("connected_at")]
    public DateTimeOffset ConnectedAt { get; set; }
}

public sealed class ResourceRequirements
{
    [JsonPropertyName("cpu_cores")]
    public uint CpuCores { get; set; }
    [JsonPropertyName("ram_gb")]
    public ulong RamGb { get; set; }
    [JsonPropertyName("gpu_required")]
    public bool GpuRequired { get; set; }
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
