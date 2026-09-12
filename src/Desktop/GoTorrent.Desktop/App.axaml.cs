using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.ApplicationLifetimes;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
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
            desktop.MainWindow = mainWindow;
            // Torrents keep transferring whether or not the window is
            // visible - closing/minimizing to the tray (MainWindow's own
            // Closing/WindowState handling) must not end the process the
            // way it would for an ordinary window-less app.
            desktop.ShutdownMode = ShutdownMode.OnExplicitShutdown;

            mainViewModel.StartAutoRefresh();
            mainViewModel.StartLiveEvents();
            mainViewModel.StartPeerRefresh();

            // Same "wait for the real event rather than pre-empt Avalonia's
            // own timing" reasoning as the start-minimized fix below - the
            // native window handle IDesktopNotifier needs to anchor a real
            // OS notification icon to is guaranteed to exist by Opened,
            // whether or not the window ends up actually visible.
            mainWindow.Opened += (_, _) =>
            {
                if (mainWindow.TryGetPlatformHandle() is { } handle)
                {
                    mainViewModel.AttachDesktopNotifier(handle.Handle);
                }
            };

            // A .torrent file or magnet: link double-clicked with this app
            // registered as the handler (Services/WindowsFileAssociationService)
            // arrives here as the first command-line argument.
            if (desktop.Args is [var argument, ..])
            {
                _ = mainViewModel.AddFromArgumentAsync(argument);
            }

            // Avalonia's classic desktop lifetime shows desktop.MainWindow
            // itself once this method returns, regardless of whether Show()
            // was called here - so "start minimized" has to undo that show
            // right after it happens (Opened), not try to pre-empt it.
            if (mainViewModel.StartMinimized)
            {
                mainWindow.Opened += (_, _) => mainWindow.Hide();
            }
        }

        base.OnFrameworkInitializationCompleted();
    }

    private void OnTrayIconClicked(object? sender, EventArgs e) => ShowMainWindow();

    private void OnTrayShowClicked(object? sender, EventArgs e) => ShowMainWindow();

    private void ShowMainWindow()
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime { MainWindow: { } window })
        {
            window.Show();
            window.WindowState = WindowState.Normal;
            window.Activate();
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
