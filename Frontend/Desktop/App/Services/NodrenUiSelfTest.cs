using System.Text;
using App.API;
using App.Models;

namespace App.Services;

public static class NodrenUiSelfTest
{
    public static async Task<int> RunAsync()
    {
        if (new JobRequest().DistributionMode != "automatic")
        {
            Console.Error.WriteLine("UI self-test failed: automatic distribution is not the default.");
            return 1;
        }
        try
        {
            _ = NodrenDistribution.ParseManualAllocations("worker-a=60,worker-b=40");
            _ = NodrenDistribution.ParseManualAllocations("worker-a=60,worker-b=30");
            Console.Error.WriteLine("UI self-test failed: invalid manual allocation was accepted.");
            return 1;
        }
        catch (FormatException)
        {
        }

        var settings = NodrenSettingsStore.Load();
        using var api = new NodrenApiClient(settings.ControllerUrl);
        try
        {
            var health = await api.GetHealthAsync();
            var nodes = await api.GetNodesAsync();
            if (nodes.Count == 0)
            {
                Console.Error.WriteLine("UI self-test failed: Controller has no registered workers.");
                return 1;
            }

            var jobs = await api.GetJobsAsync();
            var request = new JobRequest
            {
                Command = "sum",
                Priority = 50,
                Requirements = new ResourceRequirements { CpuCores = 1, RamGb = 1 },
                PayloadBase64 = Convert.ToBase64String(Encoding.UTF8.GetBytes("\x01\x02\x03")),
            };
            var submitted = await api.SubmitJobAsync(request);
            var completed = await api.WaitForJobAsync(submitted.Id, TimeSpan.FromSeconds(15));
            if (completed.Status != "COMPLETED" || completed.Result?.Value != 6)
            {
                Console.Error.WriteLine($"UI self-test failed: sum result was {completed.Result?.Value}.");
                return 1;
            }

            Console.WriteLine($"PASS: UI API self-test controller={health.Status} workers={nodes.Count} jobs={jobs.Count} result={completed.Result.Value}");
            return 0;
        }
        catch (NodrenApiException exception)
        {
            Console.Error.WriteLine($"UI self-test failed: {exception.Message}");
            return 1;
        }
    }
}
