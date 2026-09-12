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

    private void OnTrayExitClicked(object? sender, EventArgs e)
    {
        if (ApplicationLifetime is IClassicDesktopStyleApplicationLifetime desktop)
        {
            if (desktop.MainWindow is MainWindow mainWindow)
            {
                mainWindow.AllowRealClose();
            }
            desktop.Shutdown();
        }
    }
}
