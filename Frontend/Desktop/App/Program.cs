using System;
using System.Linq;
using App.Services;
using Avalonia;

namespace App;

internal sealed class Program
{
    [STAThread]
    public static void Main(string[] args)
    {
        if (args.Any(argument => string.Equals(argument, "--self-test", StringComparison.OrdinalIgnoreCase)))
        {
            Environment.ExitCode = NodrenUiSelfTest.RunAsync().GetAwaiter().GetResult();
            return;
        }

        BuildAvaloniaApp().StartWithClassicDesktopLifetime(args);
    }

    public static AppBuilder BuildAvaloniaApp()
        => AppBuilder.Configure<App>()
            .UsePlatformDetect()
#if DEBUG
            .WithDeveloperTools()
#endif
            .WithInterFont()
            .LogToTrace();
}
