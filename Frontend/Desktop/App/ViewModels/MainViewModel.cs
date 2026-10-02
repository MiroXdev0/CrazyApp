using System.Collections.ObjectModel;
using System.Text;
using App.API;
using App.Models;
using App.Services;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;

namespace App.ViewModels;

public partial class MainViewModel : ViewModelBase
{
    private readonly NodrenSettings _settings;
    private readonly NodrenApiClient _api;
    private readonly SemaphoreSlim _refreshLock = new(1, 1);

    public MainViewModel()
    {
        _settings = NodrenSettingsStore.Load();
        _controllerUrl = _settings.ControllerUrl;
        _api = new NodrenApiClient(_controllerUrl);
        _ = SynchronizationLoopAsync();
    }

    public ObservableCollection<NodeRecord> Workers { get; } = [];
    public ObservableCollection<Job> Jobs { get; } = [];
    public IReadOnlyList<string> Workloads { get; } = ["sum", "xor", "dot_product"];
    public IReadOnlyList<string> TaskTypes { get; } = ["NATIVE_WORKLOAD", "PROCESS", "COMMAND", "SCRIPT"];

    [ObservableProperty]
    private string _controllerUrl;

    [ObservableProperty]
    private string _controllerStatus = "○ Controller Offline";

    [ObservableProperty]
    private string _lastUpdated = "Not connected";

    [ObservableProperty]
    private string _errorMessage = string.Empty;

    [ObservableProperty]
    private string _workerSummary = "Workers: 0 total · 0 ready · 0 busy · 0 lost · 0 offline";

    [ObservableProperty]
    private string _jobSummary = "Jobs: 0 running · 0 queued · 0 completed · 0 failed";

    [ObservableProperty]
    private string _clusterSummary = "Cluster status unavailable.";

    [ObservableProperty]
    private string _resourceSummary = "Resource telemetry unavailable.";

    [ObservableProperty]
    private string _activitySummary = "No task activity reported.";

    [ObservableProperty]
    private string _controllerUptime = "Controller uptime unavailable.";

    [ObservableProperty]
    private bool _isRefreshing;

    [ObservableProperty]
    private string _selectedWorkload = "sum";

    [ObservableProperty]
    private string _selectedTaskType = "NATIVE_WORKLOAD";

    [ObservableProperty]
    private string _executable = string.Empty;

    [ObservableProperty]
    private string _scriptRuntime = "python";

    [ObservableProperty]
    private string _scriptPath = string.Empty;

    [ObservableProperty]
    private string _generalArguments = string.Empty;

    [ObservableProperty]
    private bool _isAutomaticDistribution = true;

    [ObservableProperty]
    private bool _isManualDistribution;

    [ObservableProperty]
    private bool _isAdvancedDistributionOpen;

    [ObservableProperty]
    private string _manualAllocationText = string.Empty;

    [ObservableProperty]
    private string _arguments = "1 2 3 4 5";

    [ObservableProperty]
    private string _leftVector = "1,2,3";

    [ObservableProperty]
    private string _rightVector = "4,5,6";

    [ObservableProperty]
    private string _runStatus = "No workload submitted.";

    [ObservableProperty]
    private string _runResult = string.Empty;

    [ObservableProperty]
    private Job? _selectedJob;

    [ObservableProperty]
    private NodeRecord? _selectedWorker;

    [ObservableProperty]
    private string _jobDetails = "Select a job to inspect its details.";

    [ObservableProperty]
    private string _distributionSummary = "Automatic distribution is selected.";

    [RelayCommand]
    private async Task RefreshAsync()
    {
        if (!await _refreshLock.WaitAsync(0))
        {
            return;
        }

        try
        {
            IsRefreshing = true;
            ErrorMessage = string.Empty;
            var healthTask = _api.GetHealthAsync();
            var nodesTask = _api.GetNodesAsync();
            var jobsTask = _api.GetJobsAsync();
            await Task.WhenAll(healthTask, nodesTask, jobsTask);

            var health = await healthTask;
            ReplaceCollection(Workers, await nodesTask);
            ReplaceCollection(Jobs, await jobsTask);
            ClusterStatus? cluster = null;
            try
            {
                cluster = await _api.GetClusterStatusAsync();
            }
            catch (NodrenApiException exception) when (exception.StatusCode == System.Net.HttpStatusCode.NotFound)
            {
                // Older Controllers still provide the original health/nodes/jobs
                // endpoints; keep the dashboard useful during rolling upgrades.
            }
            ControllerStatus = $"● Controller Online · {health.Service} {health.Version}";
            LastUpdated = $"Updated {DateTime.Now:HH:mm:ss} · {health.Nodes} workers reported by Controller";
            UpdateSummaries();
            UpdateDashboard(cluster);
            RefreshSelectedJobDetails();
        }
        catch (Exception exception) when (exception is NodrenApiException or HttpRequestException or TaskCanceledException)
        {
            ControllerStatus = "○ Controller Offline";
            ErrorMessage = exception.Message;
            LastUpdated = "Connection failed";
            Workers.Clear();
            Jobs.Clear();
            UpdateSummaries();
            ClusterSummary = "Cluster status unavailable.";
            ResourceSummary = "Resource telemetry unavailable.";
            ActivitySummary = "No task activity reported.";
            ControllerUptime = "Controller uptime unavailable.";
        }
        finally
        {
            IsRefreshing = false;
            _refreshLock.Release();
        }
    }

    [RelayCommand]
    private async Task TestConnectionAsync()
    {
        try
        {
            _settings.ControllerUrl = ControllerUrl;
            NodrenSettingsStore.Save(_settings);
            _api.SetControllerUrl(ControllerUrl);
            await RefreshAsync();
        }
        catch (Exception exception)
        {
            ControllerStatus = "○ Controller Offline";
            ErrorMessage = exception.Message;
        }
    }

    [RelayCommand]
    private void SaveSettings()
    {
        try
        {
            _settings.ControllerUrl = ControllerUrl;
            NodrenSettingsStore.Save(_settings);
            _api.SetControllerUrl(ControllerUrl);
            ErrorMessage = string.Empty;
            ControllerStatus = "○ Settings saved · connection not tested";
        }
        catch (Exception exception)
        {
            ErrorMessage = exception.Message;
        }
    }

    [RelayCommand]
    private async Task RunWorkloadAsync()
    {
        try
        {
            ErrorMessage = string.Empty;
            RunResult = string.Empty;
            if (SelectedTaskType != "NATIVE_WORKLOAD")
            {
                await RunGeneralTaskAsync();
                return;
            }
            var payload = BuildPayload(SelectedWorkload, Arguments, LeftVector, RightVector);
            RunStatus = "Submitting workload...";
            var submitted = await _api.SubmitJobAsync(new JobRequest
            {
                Command = SelectedWorkload,
                Priority = 50,
                Requirements = new ResourceRequirements { CpuCores = 1, RamGb = 1 },
                PayloadBase64 = Convert.ToBase64String(payload),
                DistributionMode = IsManualDistribution ? "manual" : "automatic",
                ManualAllocations = IsManualDistribution ? NodrenDistribution.ParseManualAllocations(ManualAllocationText) : null,
            });
            RunStatus = $"Job {submitted.Id} · {submitted.Status} · waiting for result";
            var completed = await _api.WaitForJobAsync(submitted.Id, TimeSpan.FromSeconds(60));
            if (completed.Status == "FAILED")
            {
                RunStatus = $"Job {completed.Id} failed";
                RunResult = $"{completed.Result?.ErrorCode}: {completed.Result?.Error}";
            }
            else
            {
                var workerDisplay = completed.NodeIds.Count > 0 ? string.Join(", ", completed.NodeIds) : DisplayOrDash(completed.NodeId);
                RunStatus = $"Job {completed.Id} · {completed.Status} · worker(s): {workerDisplay}";
                RunResult = $"Result: {completed.Result?.Value} · {completed.Result?.DurationUs} us";
            }

            await RefreshAsync();
        }
        catch (Exception exception) when (exception is NodrenApiException or FormatException or OverflowException or ArgumentException)
        {
            RunStatus = "Workload failed";
            ErrorMessage = exception.Message;
        }
    }

    private async Task RunGeneralTaskAsync()
    {
        var arguments = GeneralArguments.Split(' ', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).ToList();
        var task = new GeneralTaskSpec
        {
            Type = SelectedTaskType,
            Version = "1",
            Arguments = arguments,
            Requirements = new ResourceRequirements { CpuCores = 1, RamGb = 1 },
            TimeoutMs = 60_000,
        };
        if (SelectedTaskType == "SCRIPT")
        {
            task.Runtime = ScriptRuntime.Trim();
            task.Script = ScriptPath.Trim();
            if (task.Runtime.Length == 0 || task.Script.Length == 0)
            {
                throw new FormatException("Script runtime and script path are required.");
            }
        }
        else
        {
            task.Executable = Executable.Trim();
            if (task.Executable.Length == 0)
            {
                throw new FormatException("An executable path or command is required.");
            }
        }

        RunStatus = "Submitting task...";
        var submitted = await _api.SubmitTaskAsync(new TaskRequest { Task = task });
        RunStatus = $"Task {submitted.Id} · {submitted.Status} · waiting for result";
        var completed = await _api.WaitForJobAsync(submitted.Id, TimeSpan.FromSeconds(90));
        var workerDisplay = completed.NodeIds.Count > 0 ? string.Join(", ", completed.NodeIds) : DisplayOrDash(completed.NodeId);
        RunStatus = $"Task {completed.Id} · {completed.Status} · worker(s): {workerDisplay}";
        if (completed.Execution is { } execution)
        {
            var stdout = DecodeOutput(execution.StdoutBase64);
            var stderr = DecodeOutput(execution.StderrBase64);
            RunResult = $"Exit: {execution.ExitCode?.ToString() ?? "-"} · {execution.DurationUs} us\nstdout: {stdout}\nstderr: {stderr}";
        }
        await RefreshAsync();
    }

    private static string DecodeOutput(string value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return "";
        }
        try
        {
            return Encoding.UTF8.GetString(Convert.FromBase64String(value));
        }
        catch (FormatException)
        {
            return "<invalid base64 output>";
        }
    }

    [RelayCommand]
    private Task PingWorkerAsync() => WorkerActionAsync("ping");

    [RelayCommand]
    private Task PauseWorkerAsync() => WorkerActionAsync("pause");

    [RelayCommand]
    private Task ResumeWorkerAsync() => WorkerActionAsync("resume");

    [RelayCommand]
    private Task RemoveWorkerAsync() => WorkerActionAsync("remove");

    private async Task WorkerActionAsync(string action)
    {
        if (SelectedWorker is null)
        {
            ErrorMessage = "Select a worker first.";
            return;
        }

        try
        {
            ErrorMessage = string.Empty;
            SelectedWorker = await _api.WorkerActionAsync(SelectedWorker.Info.Id, action);
            await RefreshAsync();
        }
        catch (Exception exception) when (exception is NodrenApiException or HttpRequestException or TaskCanceledException)
        {
            ErrorMessage = exception.Message;
        }
    }

    [RelayCommand]
    private Task CancelJobAsync() => JobActionAsync("cancel");

    [RelayCommand]
    private Task PauseJobAsync() => JobActionAsync("pause");

    [RelayCommand]
    private Task ResumeJobAsync() => JobActionAsync("resume");

    private async Task JobActionAsync(string action)
    {
        if (SelectedJob is null)
        {
            ErrorMessage = "Select a job first.";
            return;
        }

        try
        {
            ErrorMessage = string.Empty;
            SelectedJob = await _api.JobActionAsync(SelectedJob.Id, action);
            await RefreshAsync();
        }
        catch (Exception exception) when (exception is NodrenApiException or HttpRequestException or TaskCanceledException)
        {
            ErrorMessage = exception.Message;
        }
    }

    partial void OnSelectedJobChanged(Job? value) => RefreshSelectedJobDetails();

    private async Task SynchronizationLoopAsync()
    {
        while (true)
        {
            try
            {
                await RefreshAsync();
                await _api.ListenForEventsAsync(RefreshAsync);
            }
            catch (Exception exception) when (exception is NodrenApiException or HttpRequestException or TaskCanceledException or IOException)
            {
                ControllerStatus = "○ Controller Offline";
                ErrorMessage = exception.Message;
                await Task.Delay(TimeSpan.FromSeconds(3));
            }
        }
    }

    private void UpdateSummaries()
    {
        var ready = Workers.Count(worker => worker.State == "READY");
        var busy = Workers.Count(worker => worker.State == "BUSY");
        var lost = Workers.Count(worker => worker.State == "LOST");
        var offline = Workers.Count(worker => worker.State == "OFFLINE");
        WorkerSummary = $"Workers: {Workers.Count} total · {ready} ready · {busy} busy · {lost} lost · {offline} offline";

        var running = Jobs.Count(job => job.Status == "RUNNING");
        var queued = Jobs.Count(job => job.Status == "QUEUED");
        var completed = Jobs.Count(job => job.Status == "COMPLETED");
        var paused = Jobs.Count(job => job.Status == "PAUSED");
        var failed = Jobs.Count(job => job.Status == "FAILED");
        var cancelled = Jobs.Count(job => job.Status == "CANCELLED");
        JobSummary = $"Jobs: {running} running · {queued} queued · {paused} paused · {completed} completed · {failed} failed · {cancelled} cancelled";
    }

    private void UpdateDashboard(ClusterStatus? cluster)
    {
        if (cluster is null)
        {
            ClusterSummary = WorkerSummary;
            ResourceSummary = "Live cluster telemetry is not available from this Controller.";
            ActivitySummary = JobSummary;
            ControllerUptime = "Controller uptime unavailable.";
            return;
        }

        ClusterSummary = $"{cluster.Status.ToUpperInvariant()} · {cluster.OnlineWorkers} online · {cluster.StaleWorkers} stale · {cluster.OfflineWorkers} offline · {cluster.PausedWorkers} paused";
        var cpu = cluster.CpuUtilizationPercent < 0 ? "unknown" : $"{cluster.CpuUtilizationPercent:0.#}%";
        var memory = cluster.MemoryUtilizationPercent < 0 ? "unknown" : $"{cluster.MemoryUtilizationPercent:0.#}%";
        var dynamicMemory = cluster.MemoryTelemetryWorkers == 0 ? "unknown" : $"{cluster.MemoryAvailableGb} GB";
        ResourceSummary = $"CPU usage {cpu} · RAM usage {memory} · GPU usage {cluster.GpuUtilizationPercent:0.#}% · dynamic RAM available {dynamicMemory} · scheduler capacity {cluster.AvailableCpuCores}/{cluster.TotalCpuCores} cores, {cluster.AvailableRamGb}/{cluster.TotalRamGb} GB · VRAM {cluster.AvailableVramGb}/{cluster.TotalVramGb} GB";
        ActivitySummary = $"{cluster.ActiveJobs} active jobs · {cluster.QueuedJobs} queued · {cluster.ActiveTasks} active tasks · {cluster.TotalCompletedTasks} completed tasks · {cluster.TotalFailedTasks} failed tasks · throughput {cluster.ThroughputUnitsPerSecond:0.##} units/s";
        ControllerUptime = $"Controller uptime {FormatDuration(cluster.UptimeSeconds)} · telemetry from {cluster.TelemetryWorkers} worker(s)";
    }

    private static string FormatDuration(ulong seconds)
    {
        var span = TimeSpan.FromSeconds(seconds);
        return span.TotalDays >= 1
            ? $"{(int)span.TotalDays}d {span.Hours}h {span.Minutes}m"
            : span.TotalHours >= 1
                ? $"{span.Hours}h {span.Minutes}m {span.Seconds}s"
                : $"{span.Minutes}m {span.Seconds}s";
    }

    private void RefreshSelectedJobDetails()
    {
        if (SelectedJob is null)
        {
            JobDetails = "Select a job to inspect its details.";
            return;
        }

        var result = SelectedJob.Result;
        var distribution = SelectedJob.Distribution;
        var partitionableDesc = distribution.Partitionable ? "Yes (Adaptive)" : "No (Single task)";
        var workerDisplay = SelectedJob.NodeIds.Count > 0 ? string.Join(", ", SelectedJob.NodeIds) : DisplayOrDash(SelectedJob.NodeId);
        DistributionSummary = $"{distribution.Mode} · {distribution.CompletedPartitions}/{distribution.TotalPartitions} partitions · {distribution.CompletedUnits}/{distribution.TotalUnits} units ({distribution.ProgressPercent:0.#}%)";
        var reasons = SelectedJob.Partitions.Where(partition => !string.IsNullOrWhiteSpace(partition.AssignmentReason)).Take(3).Select(partition => $"{partition.Id}: {partition.AssignmentReason}");
        JobDetails = $"Job ID: {SelectedJob.Id}\nWorkload: {SelectedJob.Command}\nState: {SelectedJob.Status}\nPartitionable: {partitionableDesc}\nDistribution: {distribution.Mode}\nPartitions: {distribution.CompletedPartitions}/{distribution.TotalPartitions} (Running: {distribution.RunningPartitions}, Pending: {distribution.PendingPartitions}, Requeued: {distribution.RequeuedPartitions}, Failed: {distribution.FailedPartitions})\nUnits: {distribution.CompletedUnits}/{distribution.TotalUnits} ({distribution.ProgressPercent:0.#}%)\nWorkers: {workerDisplay}\nQueue time: {SelectedJob.QueueTimeMs} ms\nElapsed: {SelectedJob.ElapsedMs} ms\nCPU: {SelectedJob.Requirements.CpuCores}\nRAM: {SelectedJob.Requirements.RamGb} GB\nGPU required: {SelectedJob.Requirements.GpuRequired}\nScheduler: {string.Join(" | ", reasons)}\nResult: {result?.Value.ToString() ?? "-"}\nError: {DisplayOrDash(result?.Error)}";
    }

    partial void OnIsAutomaticDistributionChanged(bool value)
    {
        if (value)
        {
            IsManualDistribution = false;
            DistributionSummary = "Automatic distribution is selected.";
        }
    }

    partial void OnIsManualDistributionChanged(bool value)
    {
        if (value)
        {
            IsAutomaticDistribution = false;
            DistributionSummary = "Manual distribution requires worker percentages totaling 100%.";
        }
    }

    private static byte[] BuildPayload(string workload, string arguments, string leftVector, string rightVector)
    {
        if (workload is "sum" or "xor")
        {
            var values = arguments.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries);
            if (values.Length == 0)
            {
                throw new FormatException($"{workload} requires one or more unsigned byte arguments.");
            }

            return values.Select(value => byte.Parse(value, System.Globalization.CultureInfo.InvariantCulture)).ToArray();
        }

        if (workload == "dot_product")
        {
            var left = ParseVector(leftVector);
            var right = ParseVector(rightVector);
            if (left.Count == 0 || left.Count != right.Count)
            {
                throw new FormatException("dot_product vectors must be non-empty and have equal length.");
            }

            using var stream = new MemoryStream();
            using var writer = new BinaryWriter(stream, Encoding.UTF8, leaveOpen: true);
            writer.Write((uint)left.Count);
            foreach (var value in left.Concat(right))
            {
                writer.Write(value);
            }
            return stream.ToArray();
        }

        throw new ArgumentException($"Unsupported workload: {workload}");
    }

    private static List<int> ParseVector(string value)
        => value.Split(',', StringSplitOptions.RemoveEmptyEntries)
            .Select(part => int.Parse(part.Trim(), System.Globalization.CultureInfo.InvariantCulture))
            .ToList();

    private static string DisplayOrDash(string? value) => string.IsNullOrWhiteSpace(value) ? "-" : value;

    private static void ReplaceCollection<T>(ObservableCollection<T> target, IEnumerable<T> values)
    {
        target.Clear();
        foreach (var value in values)
        {
            target.Add(value);
        }
    }
}
