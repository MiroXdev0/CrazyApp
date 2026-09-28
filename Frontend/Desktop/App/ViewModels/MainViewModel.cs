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
        _ = PollLoopAsync();
    }

    public ObservableCollection<NodeRecord> Workers { get; } = [];
    public ObservableCollection<Job> Jobs { get; } = [];
    public IReadOnlyList<string> Workloads { get; } = ["sum", "xor", "dot_product"];

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
    private bool _isRefreshing;

    [ObservableProperty]
    private string _selectedWorkload = "sum";

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
            ControllerStatus = $"● Controller Online · {health.Service} {health.Version}";
            LastUpdated = $"Updated {DateTime.Now:HH:mm:ss} · {health.Nodes} workers reported by Controller";
            UpdateSummaries();
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
                RunStatus = $"Job {completed.Id} · {completed.Status} · worker {completed.NodeId}";
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

    partial void OnSelectedJobChanged(Job? value) => RefreshSelectedJobDetails();

    private async Task PollLoopAsync()
    {
        while (true)
        {
            await RefreshAsync();
            await Task.Delay(TimeSpan.FromSeconds(5));
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
        var failed = Jobs.Count(job => job.Status == "FAILED");
        JobSummary = $"Jobs: {running} running · {queued} queued · {completed} completed · {failed} failed";
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
        DistributionSummary = $"{distribution.Mode} · {distribution.CompletedPartitions}/{distribution.TotalPartitions} partitions · {distribution.CompletedUnits}/{distribution.TotalUnits} units";
        JobDetails = $"Job ID: {SelectedJob.Id}\nWorkload: {SelectedJob.Command}\nState: {SelectedJob.Status}\nDistribution: {distribution.Mode}\nPartitions: {distribution.CompletedPartitions}/{distribution.TotalPartitions}\nUnits: {distribution.CompletedUnits}/{distribution.TotalUnits}\nWorkers: {DisplayOrDash(string.Join(", ", SelectedJob.NodeIds))}\nCPU: {SelectedJob.Requirements.CpuCores}\nRAM: {SelectedJob.Requirements.RamGb} GB\nGPU required: {SelectedJob.Requirements.GpuRequired}\nResult: {result?.Value.ToString() ?? "-"}\nError: {DisplayOrDash(result?.Error)}";
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
