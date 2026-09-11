using Avalonia.Controls;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

public partial class MainWindow : Window
{
    public MainWindow()
    {
        InitializeComponent();
    }

    private async void OnAddTorrentClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }
        var dialog = new AddTorrentWindow(mainViewModel);
        await dialog.ShowDialog(this);
    }
}