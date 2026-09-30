using Avalonia.Controls;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// A read-only live display, same shape as <see cref="StatisticsWindow"/>
/// - binds its <see cref="DataContext"/> straight to <see cref="MainViewModel"/>
/// rather than the no-ViewModel code-behind pattern the one-shot form
/// dialogs use. <see cref="MainViewModel.LoadActivityHistoryAsync"/> is
/// what's actually unit-tested; this window is pure chrome around it.
/// </summary>
public partial class ActivityHistoryWindow : Window
{
    private readonly MainViewModel _mainViewModel;

    public ActivityHistoryWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
    }

    public ActivityHistoryWindow(MainViewModel mainViewModel)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        DataContext = mainViewModel;
        Opened += async (_, _) => await _mainViewModel.LoadActivityHistoryAsync();
    }

    private void OnCloseClick(object? sender, RoutedEventArgs e) => Close();
}
