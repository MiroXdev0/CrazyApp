using System;
using System.Linq;
using App.Services;
using Avalonia;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Threading;

namespace App;

internal sealed class Program
{
    [STAThread]
    public static void Main(string[] args)
    {
        if (args.Any(argument => string.Equals(argument, "--shutdown", StringComparison.OrdinalIgnoreCase)))
        {
            try
            {
                InstanceControl.RequestShutdownAsync().GetAwaiter().GetResult();
            }
            catch (Exception error)
            {
                ShowError($"Nodren could not request application shutdown: {error.Message}");
                Environment.ExitCode = 1;
            }
            return;
        }

        if (args.Any(argument => string.Equals(argument, "--self-test", StringComparison.OrdinalIgnoreCase)))
        {
            Environment.ExitCode = NodrenUiSelfTest.RunAsync().GetAwaiter().GetResult();
            return;
        }

        using var appMutex = new Mutex(false, @"Global\Nodren.Norden");
        var ownsMutex = false;
        try
        {
            try
            {
                ownsMutex = appMutex.WaitOne(0);
            }
            catch (AbandonedMutexException)
            {
                ownsMutex = true;
            }

            if (!ownsMutex)
            {
                return;
            }

            using var controller = ManagedController.StartAsync().GetAwaiter().GetResult();
            using var shutdownCancellation = new CancellationTokenSource();
            var shutdownListener = InstanceControl.ListenAsync(shutdownCancellation.Token);
            try
            {
                BuildAvaloniaApp().StartWithClassicDesktopLifetime(args);
            }
            finally
            {
                shutdownCancellation.Cancel();
                shutdownListener.GetAwaiter().GetResult();
                try
                {
                    controller.StopAsync().GetAwaiter().GetResult();
                }
                catch (Exception error)
                {
                    ShowError($"Nodren could not shut down its managed Controller cleanly: {error.Message}");
                    Environment.ExitCode = 1;
                }
            }
        }
        catch (Exception error)
        {
            ShowError($"Nodren could not start: {error.Message}");
            Environment.ExitCode = 1;
        }
        finally
        {
            if (ownsMutex)
            {
                appMutex.ReleaseMutex();
            }
        }
    }

    public static AppBuilder BuildAvaloniaApp()
        => AppBuilder.Configure<App>()
            .UsePlatformDetect()
#if DEBUG
            .WithDeveloperTools()
#endif
            .WithInterFont()
            .LogToTrace();

    private static void ShowError(string message)
    {
        if (OperatingSystem.IsWindows())
        {
            _ = MessageBoxW(IntPtr.Zero, message, "Nodren", 0x10);
            return;
        }

        Console.Error.WriteLine(message);
    }

    [System.Runtime.InteropServices.DllImport("user32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
    private static extern int MessageBoxW(IntPtr window, string text, string caption, uint type);
}
