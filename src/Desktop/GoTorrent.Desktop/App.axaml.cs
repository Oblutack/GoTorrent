using Avalonia;
using Avalonia.Controls.ApplicationLifetimes;
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
            desktop.MainWindow = new MainWindow
            {
                DataContext = mainViewModel,
            };
            mainViewModel.StartAutoRefresh();
            mainViewModel.StartLiveEvents();
        }

        base.OnFrameworkInitializationCompleted();
    }
}