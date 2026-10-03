using System.Text.Json;

namespace App.Services;

public sealed class NodrenSettings
{
    public string ControllerUrl { get; set; } = "http://127.0.0.1:8080";
    public bool IsDarkTheme { get; set; }
}

public static class NodrenSettingsStore
{
    private static string SettingsPath => Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
        "Nodren",
        "settings.json");

    public static NodrenSettings Load()
    {
        var settings = new NodrenSettings();
        try
        {
            if (File.Exists(SettingsPath))
            {
                var storedSettings = JsonSerializer.Deserialize<NodrenSettings>(File.ReadAllText(SettingsPath));
                if (storedSettings is not null && !string.IsNullOrWhiteSpace(storedSettings.ControllerUrl))
                {
                    settings = storedSettings;
                }
            }
        }
        catch (IOException)
        {
        }
        catch (JsonException)
        {
        }

        var configured = Environment.GetEnvironmentVariable("NODREN_MANAGED_CONTROLLER_URL")
            ?? Environment.GetEnvironmentVariable("NODREN_CONTROLLER_URL")
            ?? Environment.GetEnvironmentVariable("NODREN_HTTP_ADDR");
        if (!string.IsNullOrWhiteSpace(configured))
        {
            settings.ControllerUrl = Normalize(configured);
        }

        return settings;
    }

    public static void Save(NodrenSettings settings)
    {
        var directory = Path.GetDirectoryName(SettingsPath)!;
        Directory.CreateDirectory(directory);
        File.WriteAllText(SettingsPath, JsonSerializer.Serialize(settings, new JsonSerializerOptions { WriteIndented = true }));
    }

    private static string Normalize(string value)
    {
        value = value.Trim();
        return value.StartsWith("http://", StringComparison.OrdinalIgnoreCase) ||
               value.StartsWith("https://", StringComparison.OrdinalIgnoreCase)
            ? value
            : "http://" + value;
    }
}
