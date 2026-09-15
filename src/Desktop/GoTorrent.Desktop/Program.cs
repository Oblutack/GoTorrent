using Avalonia;
using System;
using System.Threading.Tasks;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop;

sealed class Program
{
    // Initialization code. Don't use any Avalonia, third-party APIs or any
    // SynchronizationContext-reliant code before AppMain is called: things aren't initialized
    // yet and stuff might break.
    //
    // Stage 4's single-instance enforcement: SingleInstanceGuard's named
    // Mutex is safe to touch this early (plain BCL, no Avalonia/UI
    // dependency), and has to run before StartWithClassicDesktopLifetime -
    // a second launch that turns out not to be primary must never
    // construct any Avalonia UI at all, not construct-then-tear-down one.
    [STAThread]
    public static void Main(string[] args)
    {
        using var guard = new SingleInstanceGuard();
        if (!guard.IsPrimaryInstance)
        {
            ForwardToRunningInstanceAsync(args).GetAwaiter().GetResult();
            return;
        }

        App.SingleInstanceGuard = guard;
        BuildAvaloniaApp().StartWithClassicDesktopLifetime(args);
    }

    /// <summary>
    /// Forwards every argument, not just the first - a real, related bug
    /// this same Stage 4 item fixes alongside single-instance itself:
    /// the file-association launch path used to only ever read
    /// <c>desktop.Args[0]</c>, silently dropping the rest of a multi-file
    /// selection. A launch with no arguments at all (a bare double-click
    /// of the exe) still forwards <see cref="SingleInstanceGuard.ShowSignal"/>,
    /// so the running instance surfaces its window instead of the second
    /// launch silently doing nothing.
    /// </summary>
    private static async Task ForwardToRunningInstanceAsync(string[] args)
    {
        if (args.Length == 0)
        {
            await SingleInstanceGuard.TryForwardArgumentAsync(SingleInstanceGuard.ShowSignal);
            return;
        }
        foreach (var argument in args)
        {
            await SingleInstanceGuard.TryForwardArgumentAsync(argument);
        }
    }

    // Avalonia configuration, don't remove; also used by visual designer.
    public static AppBuilder BuildAvaloniaApp()
        => AppBuilder.Configure<App>()
            .UsePlatformDetect()
#if DEBUG
            .WithDeveloperTools()
#endif
            .WithInterFont()
            .LogToTrace();
}
