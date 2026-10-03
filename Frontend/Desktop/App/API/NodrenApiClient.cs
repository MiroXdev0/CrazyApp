using System.Net;
using System.Net.Http.Json;
using System.Text.Json;
using App.Models;

namespace App.API;

public sealed class NodrenApiException : Exception
{
    public NodrenApiException(string message, HttpStatusCode? statusCode = null, Exception? innerException = null)
        : base(message, innerException)
    {
        StatusCode = statusCode;
    }

    public HttpStatusCode? StatusCode { get; }
}

public sealed class NodrenApiClient : IDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        PropertyNameCaseInsensitive = true,
    };

    private readonly HttpClient _httpClient;

    public NodrenApiClient(string controllerUrl)
    {
        var apiToken = Environment.GetEnvironmentVariable("NODREN_API_TOKEN");
        var handler = new HttpClientHandler
        {
            AllowAutoRedirect = string.IsNullOrWhiteSpace(apiToken),
        };
        _httpClient = new HttpClient(handler)
        {
            Timeout = TimeSpan.FromSeconds(10),
        };
        if (!string.IsNullOrWhiteSpace(apiToken))
        {
            _httpClient.DefaultRequestHeaders.Authorization =
                new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", apiToken);
        }
        SetControllerUrl(controllerUrl);
    }

    public Uri ControllerUri => _httpClient.BaseAddress!;

    public void SetControllerUrl(string controllerUrl)
    {
        var value = (controllerUrl ?? string.Empty).Trim();
        if (!value.StartsWith("http://", StringComparison.OrdinalIgnoreCase) &&
            !value.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
        {
            value = "http://" + value;
        }

        if (!Uri.TryCreate(value.TrimEnd('/') + "/", UriKind.Absolute, out var uri) ||
            (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps))
        {
            throw new NodrenApiException($"Invalid Controller URL: {controllerUrl}");
        }

        var apiToken = Environment.GetEnvironmentVariable("NODREN_API_TOKEN");
        if (!string.IsNullOrWhiteSpace(apiToken) &&
            uri.Scheme == Uri.UriSchemeHttp &&
            !uri.IsLoopback)
        {
            throw new NodrenApiException("Refusing to send NODREN_API_TOKEN over a non-HTTPS Controller URL");
        }

        _httpClient.BaseAddress = uri;
    }

    public Task<HealthResponse> GetHealthAsync(CancellationToken cancellationToken = default)
        => GetAsync<HealthResponse>("health", cancellationToken);

    public Task<ClusterStatus> GetClusterStatusAsync(CancellationToken cancellationToken = default)
        => GetAsync<ClusterStatus>("v1/cluster/status", cancellationToken);

    public Task<List<NodeRecord>> GetNodesAsync(CancellationToken cancellationToken = default)
        => GetAsync<List<NodeRecord>>("v1/nodes", cancellationToken);

    public Task<List<Job>> GetJobsAsync(CancellationToken cancellationToken = default)
        => GetAsync<List<Job>>("v1/jobs", cancellationToken);

    public Task<List<Job>> GetTasksAsync(CancellationToken cancellationToken = default)
        => GetAsync<List<Job>>("v1/tasks", cancellationToken);

    public Task<Job> GetJobAsync(string jobId, CancellationToken cancellationToken = default)
        => GetAsync<Job>($"v1/jobs/{Uri.EscapeDataString(jobId)}", cancellationToken);

    public Task<NodeRecord> WorkerActionAsync(string workerId, string action, CancellationToken cancellationToken = default)
        => PostAsync<NodeRecord>($"v1/nodes/{Uri.EscapeDataString(workerId)}/{action}", cancellationToken);

    public Task<Job> JobActionAsync(string jobId, string action, CancellationToken cancellationToken = default)
        => PostAsync<Job>($"v1/jobs/{Uri.EscapeDataString(jobId)}/{action}", cancellationToken);

    public async Task<Job> UpdateDistributionAsync(string jobId, DistributionUpdateRequest request, CancellationToken cancellationToken = default)
    {
        try
        {
            using var response = await _httpClient.PutAsJsonAsync($"v1/jobs/{Uri.EscapeDataString(jobId)}/distribution", request, JsonOptions, cancellationToken);
            return await ReadResponseAsync<Job>(response, cancellationToken);
        }
        catch (NodrenApiException)
        {
            throw;
        }
        catch (HttpRequestException exception)
        {
            throw Unavailable(exception);
        }
        catch (TaskCanceledException exception) when (!cancellationToken.IsCancellationRequested)
        {
            throw new NodrenApiException($"Controller request timed out: {ControllerUri}", null, exception);
        }
    }

    public async Task ListenForEventsAsync(Func<Task> onEvent, CancellationToken cancellationToken = default)
    {
        try
        {
            using var response = await _httpClient.GetAsync("v1/events", HttpCompletionOption.ResponseHeadersRead, cancellationToken);
            if (!response.IsSuccessStatusCode)
            {
                var body = (await response.Content.ReadAsStringAsync(cancellationToken)).Trim();
                throw new NodrenApiException($"Controller returned HTTP {(int)response.StatusCode}: {(string.IsNullOrWhiteSpace(body) ? response.ReasonPhrase : body)}", response.StatusCode);
            }

            await using var stream = await response.Content.ReadAsStreamAsync(cancellationToken);
            using var reader = new StreamReader(stream);
            while (!cancellationToken.IsCancellationRequested)
            {
                var line = await reader.ReadLineAsync(cancellationToken);
                if (line is null)
                {
                    return;
                }

                if (line.StartsWith("data:", StringComparison.Ordinal))
                {
                    await onEvent();
                }
            }
        }
        catch (NodrenApiException)
        {
            throw;
        }
        catch (HttpRequestException exception)
        {
            throw Unavailable(exception);
        }
        catch (TaskCanceledException exception) when (!cancellationToken.IsCancellationRequested)
        {
            throw new NodrenApiException($"Controller event stream timed out: {ControllerUri}", null, exception);
        }
    }

    public async Task<Job> SubmitJobAsync(JobRequest request, CancellationToken cancellationToken = default)
    {
        try
        {
            using var response = await _httpClient.PostAsJsonAsync("v1/jobs", request, JsonOptions, cancellationToken);
            return await ReadResponseAsync<Job>(response, cancellationToken);
        }
        catch (NodrenApiException)
        {
            throw;
        }
        catch (HttpRequestException exception)
        {
            throw Unavailable(exception);
        }
        catch (TaskCanceledException exception) when (!cancellationToken.IsCancellationRequested)
        {
            throw new NodrenApiException($"Controller request timed out: {ControllerUri}", null, exception);
        }
    }

    public async Task<Job> SubmitTaskAsync(TaskRequest request, CancellationToken cancellationToken = default)
    {
        try
        {
            using var response = await _httpClient.PostAsJsonAsync("v1/tasks", request, JsonOptions, cancellationToken);
            return await ReadResponseAsync<Job>(response, cancellationToken);
        }
        catch (NodrenApiException)
        {
            throw;
        }
        catch (HttpRequestException exception)
        {
            throw Unavailable(exception);
        }
        catch (TaskCanceledException exception) when (!cancellationToken.IsCancellationRequested)
        {
            throw new NodrenApiException($"Controller request timed out: {ControllerUri}", null, exception);
        }
    }

    public Task<Job> TaskActionAsync(string taskId, string action, CancellationToken cancellationToken = default)
        => PostAsync<Job>($"v1/tasks/{Uri.EscapeDataString(taskId)}/{action}", cancellationToken);

    public async Task<Job> WaitForJobAsync(string jobId, TimeSpan timeout, CancellationToken cancellationToken = default)
    {
        var deadline = DateTimeOffset.UtcNow + timeout;
        while (true)
        {
            var job = await GetJobAsync(jobId, cancellationToken);
            if (job.Status is "COMPLETED" or "FAILED" or "CANCELLED" or "TIMED_OUT")
            {
                return job;
            }

            if (DateTimeOffset.UtcNow >= deadline)
            {
                throw new NodrenApiException($"Timed out waiting for job {jobId} after {timeout.TotalSeconds:0}s");
            }

            await Task.Delay(TimeSpan.FromMilliseconds(250), cancellationToken);
        }
    }

    private async Task<T> GetAsync<T>(string path, CancellationToken cancellationToken)
    {
        try
        {
            using var response = await _httpClient.GetAsync(path, cancellationToken);
            return await ReadResponseAsync<T>(response, cancellationToken);
        }
        catch (NodrenApiException)
        {
            throw;
        }
        catch (HttpRequestException exception)
        {
            throw Unavailable(exception);
        }
        catch (TaskCanceledException exception) when (!cancellationToken.IsCancellationRequested)
        {
            throw new NodrenApiException($"Controller request timed out: {ControllerUri}", null, exception);
        }
    }

    private async Task<T> PostAsync<T>(string path, CancellationToken cancellationToken)
    {
        try
        {
            using var response = await _httpClient.PostAsync(path, null, cancellationToken);
            return await ReadResponseAsync<T>(response, cancellationToken);
        }
        catch (NodrenApiException)
        {
            throw;
        }
        catch (HttpRequestException exception)
        {
            throw Unavailable(exception);
        }
        catch (TaskCanceledException exception) when (!cancellationToken.IsCancellationRequested)
        {
            throw new NodrenApiException($"Controller request timed out: {ControllerUri}", null, exception);
        }
    }

    private async Task<T> ReadResponseAsync<T>(HttpResponseMessage response, CancellationToken cancellationToken)
    {
        var body = await response.Content.ReadAsStringAsync(cancellationToken);
        if (!response.IsSuccessStatusCode)
        {
            var detail = string.IsNullOrWhiteSpace(body) ? response.ReasonPhrase : body.Trim();
            throw new NodrenApiException($"Controller returned HTTP {(int)response.StatusCode}: {detail}", response.StatusCode);
        }

        try
        {
            return JsonSerializer.Deserialize<T>(body, JsonOptions)
                ?? throw new NodrenApiException("Invalid Controller response: empty JSON body");
        }
        catch (JsonException exception)
        {
            throw new NodrenApiException($"Invalid Controller response: {exception.Message}", response.StatusCode, exception);
        }
    }

    private NodrenApiException Unavailable(Exception exception)
        => new($"Controller unavailable at {ControllerUri}: {exception.Message}", null, exception);

    public void Dispose() => _httpClient.Dispose();
}
