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
        var validAlloc = NodrenDistribution.ParseManualAllocations("worker-a=60,worker-b=40");
        if (validAlloc["worker-a"] != 60 || validAlloc["worker-b"] != 40)
        {
            Console.Error.WriteLine("UI self-test failed: valid manual allocation was parsed incorrectly.");
            return 1;
        }

        string[] invalidCases = [
            "worker-a=60,worker-b=30", // sum != 100
            "worker-a=50,worker-a=50", // duplicate
            "worker-a=0,worker-b=100", // 0 percent
            "worker-a=150",            // > 100 percent
            "invalid-entry",           // malformed
            "",                        // empty
        ];
        foreach (var invalidCase in invalidCases)
        {
            try
            {
                _ = NodrenDistribution.ParseManualAllocations(invalidCase);
                Console.Error.WriteLine($"UI self-test failed: invalid manual allocation was accepted: {invalidCase}");
                return 1;
            }
            catch (FormatException)
            {
            }
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
