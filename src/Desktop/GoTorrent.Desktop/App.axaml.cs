using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using Avalonia.Threading;
using GoTorrent.Desktop.ViewModels;
using GoTorrent.Desktop.Views;

namespace GoTorrent.Desktop;

public partial class App : Application
{
    public override void Initialize()
    {
        AvaloniaXamlLoader.Load(this);
    }

    public override void OnFrameworkInitializationCompleted()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            var mainViewModel = new MainViewModel();
            var mainWindow = new MainWindow
            {
                DataContext = mainViewModel,
            };
            mainWindow.AttachViewModel(mainViewModel);
            // Torrents keep transferring whether or not the window is
            // visible - closing/minimizing to the tray (MainWindow's own
            // Closing/WindowState handling) must not end the process the
            // way it would for an ordinary window-less app.
            desktop.ShutdownMode = ShutdownMode.OnExplicitShutdown;

            mainViewModel.StartAutoRefresh();
            mainViewModel.StartLiveEvents();
            mainViewModel.StartPeerRefresh();

            // The native platform handle exists immediately once the Window
            // is constructed - confirmed live, not assumed - so attaching
            // here needs no Show()/Opened round trip at all, and works
            // whether or not the window is ever actually shown (the
            // start-minimized case below might never show it this run).
            if (mainWindow.TryGetPlatformHandle() is { } handle)
            {
                mainViewModel.AttachDesktopNotifier(handle.Handle);
            }

            // A .torrent file or magnet: link double-clicked with this app
            // registered as the handler (Services/WindowsFileAssociationService)
            // arrives here as the first command-line argument.
            if (desktop.Args is [var argument, ..])
            {
                _ = mainViewModel.AddFromArgumentAsync(argument);
            }

            // Avalonia's classic desktop lifetime calls MainWindow.Show()
            // unconditionally as soon as this method returns (confirmed
            // against the actual framework source, not assumed) - it does
            // not check WindowState or IsVisible first. That makes a real,
            // confirmed-live flash unavoidable with a show-then-hide
            // approach for "start minimized" (true minimize-to-tray, no
            // taskbar entry). The only flash-free option is to never let
            // the framework show it in the first place: leave
            // desktop.MainWindow unset here and assign it from a
            // Dispatcher.UIThread.Post callback instead, which only runs
            // once the dispatcher's main loop is pumping - strictly after
            // Start()'s own synchronous Show() call already ran against a
            // still-null MainWindow (a no-op). By the time the assignment
            // executes, the window is fully wired up but has never been
            // shown - exactly the state a later real "Show" click from the
            // tray should start from.
            if (mainViewModel.StartMinimized)
            {
                Dispatcher.UIThread.Post(() => desktop.MainWindow = mainWindow, DispatcherPriority.Background);
            }
            else
            {
                desktop.MainWindow = mainWindow;
            }
        }

        base.OnFrameworkInitializationCompleted();
    }

    private void OnTrayIconClicked(object? sender, EventArgs e) => ShowMainWindow();

    private void OnTrayShowClicked(object? sender, EventArgs e) => ShowMainWindow();

    private void ShowMainWindow()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime { MainWindow: MainWindow window })
        {
            window.ShowAtRestoredState();
        }
    }

    private async void OnTrayExitClicked(object? sender, EventArgs e)
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            // Daemon supervision's "offer to keep it running on exit" -
            // only asked when this instance actually spawned gottrentd
            // itself, never for one it just attached to.
            if (desktop.MainWindow is MainWindow { DataContext: MainViewModel { WeOwnRunningDaemon: true } mainViewModel })
            {
                await ConfirmStopDaemonAsync(mainViewModel);
            }

            if (desktop.MainWindow is MainWindow mainWindow)
            {
                mainWindow.AllowRealClose();
            }
            // Stops the auto-refresh/peer-refresh timers and the live
            // event socket loop, and disposes the current engine client's
            // real HttpClient - only on an actual quit, never on the
            // ordinary hide-to-tray close this handler is not on the path
            // for.
            if (desktop.MainWindow?.DataContext is MainViewModel viewModelToDispose)
            {
                viewModelToDispose.Dispose();
            }
            desktop.Shutdown();
        }
    }

    /// <summary>
    /// Shown via <see cref="Window.Show()"/>, not <c>ShowDialog</c> - the
    /// main window may be hidden (minimized to tray) right now, and this
    /// prompt must not depend on it being visible.
    /// </summary>
    private static async Task ConfirmStopDaemonAsync(MainViewModel mainViewModel)
    {
        var dialog = new ConfirmStopDaemonWindow();
        var closed = new TaskCompletionSource();
        dialog.Closed += (_, _) => closed.TrySetResult();
        dialog.Show();
        dialog.Activate();
        await closed.Task;
        if (dialog.ShouldStopDaemon)
        {
            mainViewModel.StopLocalDaemon();
        }
    }
}
