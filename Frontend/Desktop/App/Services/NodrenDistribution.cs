using System.Globalization;

namespace App.Services;

public static class NodrenDistribution
{
    public static Dictionary<string, byte> ParseManualAllocations(string value)
    {
        var allocations = new Dictionary<string, byte>(StringComparer.OrdinalIgnoreCase);
        foreach (var item in value.Split([',', ';', '\n', '\r'], StringSplitOptions.RemoveEmptyEntries))
        {
            var parts = item.Split('=', 2, StringSplitOptions.TrimEntries);
            if (parts.Length != 2 || string.IsNullOrWhiteSpace(parts[0]))
            {
                throw new FormatException("Manual distribution entries must use worker-id=percent.");
            }

            if (!byte.TryParse(parts[1], NumberStyles.Integer, CultureInfo.InvariantCulture, out var percentage) || percentage == 0 || percentage > 100)
            {
                throw new FormatException($"Invalid manual allocation for worker {parts[0]}: {parts[1]}.");
            }
            if (!allocations.TryAdd(parts[0], percentage))
            {
                throw new FormatException($"Worker {parts[0]} appears more than once in manual distribution.");
            }
        }

        if (allocations.Count == 0 || allocations.Values.Sum(value => (int)value) != 100)
        {
            throw new FormatException("Manual worker allocations must total exactly 100 percent.");
        }
        return allocations;
    }
}
