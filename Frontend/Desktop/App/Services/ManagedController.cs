using System.Diagnostics;
using System.Net;
using System.Net.Http.Headers;
using System.Reflection;
using System.Security.Cryptography;
using System.Text.Json;

namespace App.Services;

internal sealed class ManagedController : IDisposable
{
    private const string ControllerResource = "Nodren.Controller.exe";
    private const string ControllerUrl = "http://127.0.0.1:8080";
    private const string ControllerNodeAddress = ":9000";

    private readonly HttpClient _httpClient = new() { Timeout = TimeSpan.FromSeconds(1) };
    private readonly string _shutdownToken;
    private Process? _process;
    private bool _stopped;

    private ManagedController(string shutdownToken)
    {
        _shutdownToken = shutdownToken;
    }

    public static async Task<ManagedController> StartAsync()
    {
        var shutdownToken = Convert.ToHexString(RandomNumberGenerator.GetBytes(32));
        var controller = new ManagedController(shutdownToken);
        try
        {
            Environment.SetEnvironmentVariable("NODREN_MANAGED_CONTROLLER_URL", ControllerUrl);
            if (await controller.IsControllerReadyAsync())
            {
                return controller;
            }

            var dataDirectory = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
                "Nodren");
            Directory.CreateDirectory(dataDirectory);
            var controllerPath = Path.Combine(dataDirectory, "controller.exe");
            await ExtractControllerAsync(controllerPath);

            var startInfo = new ProcessStartInfo(controllerPath)
            {
                WorkingDirectory = dataDirectory,
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
            };
            startInfo.ArgumentList.Add("serve");
            startInfo.Environment["NODREN_HTTP_ADDR"] = "127.0.0.1:8080";
            startInfo.Environment["NODREN_NODE_ADDR"] = ControllerNodeAddress;
            startInfo.Environment["NODREN_STATE_FILE"] = Path.Combine(dataDirectory, "controller-state.json");
            startInfo.Environment["NODREN_SHUTDOWN_TOKEN"] = shutdownToken;

            var process = Process.Start(startInfo)
                ?? throw new InvalidOperationException("The embedded Controller process could not be started.");
            controller._process = process;
            process.OutputDataReceived += (_, eventArgs) => AppendLog(dataDirectory, "controller.stdout.log", eventArgs.Data);
            process.ErrorDataReceived += (_, eventArgs) => AppendLog(dataDirectory, "controller.stderr.log", eventArgs.Data);
            process.BeginOutputReadLine();
            process.BeginErrorReadLine();

            var deadline = Stopwatch.StartNew();
            while (deadline.Elapsed < TimeSpan.FromSeconds(15))
            {
                if (process.HasExited)
                {
                    throw new InvalidOperationException(
                        $"The Controller exited with code {process.ExitCode}. {ReadControllerLog(dataDirectory)}");
                }
                if (await controller.IsControllerReadyAsync())
                {
                    return controller;
                }
                await Task.Delay(200);
            }

            throw new TimeoutException($"The Controller did not become ready. {ReadControllerLog(dataDirectory)}");
        }
        catch
        {
            controller.Dispose();
            throw;
        }
    }

    public async Task StopAsync()
    {
        if (_stopped)
        {
            return;
        }
        var process = _process;
        if (process is null)
        {
            _stopped = true;
            _httpClient.Dispose();
            return;
        }

        if (!process.HasExited)
        {
            try
            {
                using var request = new HttpRequestMessage(HttpMethod.Post, $"{ControllerUrl}/internal/shutdown");
                request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", _shutdownToken);
                using var response = await _httpClient.SendAsync(request).ConfigureAwait(false);
                if (response.StatusCode != HttpStatusCode.Accepted)
                {
                    throw new InvalidOperationException(
                        $"The Controller rejected its shutdown request with HTTP {(int)response.StatusCode}.");
                }
            }
            catch (HttpRequestException error)
            {
                throw new InvalidOperationException("The managed Controller could not be reached for shutdown.", error);
            }

            try
            {
                await process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(10)).ConfigureAwait(false);
            }
            catch (TimeoutException)
            {
                process.Kill(entireProcessTree: true);
                await process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(5)).ConfigureAwait(false);
                throw new TimeoutException("The Controller did not stop after its graceful shutdown request and was terminated.");
            }
        }

        _process = null;
        _stopped = true;
        process.Dispose();
        _httpClient.Dispose();
    }

    public void Dispose()
    {
        if (!_stopped && _process is not null && !_process.HasExited)
        {
            _process.Kill(entireProcessTree: true);
            _process.WaitForExit(5000);
        }
        _process?.Dispose();
        _httpClient.Dispose();
        _stopped = true;
    }

    private async Task<bool> IsControllerReadyAsync()
    {
        try
        {
            using var response = await _httpClient.GetAsync($"{ControllerUrl}/health").ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                return false;
            }

            await using var stream = await response.Content.ReadAsStreamAsync().ConfigureAwait(false);
            using var document = await JsonDocument.ParseAsync(stream).ConfigureAwait(false);
            var root = document.RootElement;
            return root.TryGetProperty("status", out var status) && status.GetString() == "ok"
                && root.TryGetProperty("service", out var service) && service.GetString() == "nodren-controller";
        }
        catch (HttpRequestException)
        {
            return false;
        }
        catch (TaskCanceledException)
        {
            return false;
        }
    }

    private static async Task ExtractControllerAsync(string destination)
    {
        await using var resource = Assembly.GetExecutingAssembly().GetManifestResourceStream(ControllerResource)
            ?? throw new InvalidOperationException("The embedded Controller is missing from Norden.exe.");
        await using var output = new FileStream(destination, FileMode.Create, FileAccess.Write, FileShare.None);
        await resource.CopyToAsync(output);
    }

    private static void AppendLog(string directory, string fileName, string? line)
    {
        if (line is not null)
        {
            File.AppendAllText(Path.Combine(directory, fileName), line + Environment.NewLine);
        }
    }

    private static string ReadControllerLog(string directory)
    {
        var logPath = Path.Combine(directory, "controller.stderr.log");
        return File.Exists(logPath) ? File.ReadAllText(logPath) : "No Controller error log was produced.";
    }
}
