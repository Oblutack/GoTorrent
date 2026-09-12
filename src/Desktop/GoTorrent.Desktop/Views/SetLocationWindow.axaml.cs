using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Platform.Storage;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// A "move this torrent's data" prompt - same no-ViewModel plain-code-behind
/// pattern as <see cref="SetCategoryWindow"/>. Pre-filled with the
/// currently selected torrent's real save path (from
/// <see cref="MainViewModel.DetailTorrent"/>, already loaded by the time a
/// context-menu action can open this), unlike
/// <see cref="SetSpeedLimitsWindow"/> - see
/// <see cref="MainViewModel.SetLocationAsync"/>'s own doc comment for why
/// this one can pre-fill and that one can't.
/// </summary>
public partial class SetLocationWindow : Window
{
    private readonly MainViewModel _mainViewModel;
    private readonly string _infoHash;

    public SetLocationWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
        _infoHash = string.Empty;
    }

    public SetLocationWindow(MainViewModel mainViewModel, string infoHash, string? currentDownloadDir)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        _infoHash = infoHash;
        LocationBox.Text = currentDownloadDir;
    }

    private async void OnBrowseClick(object? sender, RoutedEventArgs e)
    {
        var topLevel = GetTopLevel(this);
        if (topLevel is null)
        {
            return;
        }

        var folders = await topLevel.StorageProvider.OpenFolderPickerAsync(new FolderPickerOpenOptions
        {
            Title = "Select a new location",
            AllowMultiple = false,
        });

        if (folders.Count > 0)
        {
            LocationBox.Text = folders[0].Path.LocalPath;
        }
    }

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnOkClick(object? sender, RoutedEventArgs e)
    {
        if (string.IsNullOrWhiteSpace(LocationBox.Text))
        {
            ShowError("Enter or browse to a folder.");
            return;
        }

        OkButton.IsEnabled = false;
        var ok = await _mainViewModel.SetLocationAsync(_infoHash, LocationBox.Text.Trim());
        if (ok)
        {
            Close();
            return;
        }
        ShowError(_mainViewModel.ConnectionError ?? "Could not move the torrent's data.");
        OkButton.IsEnabled = true;
    }

    private void ShowError(string message)
    {
        ErrorText.Text = message;
        ErrorText.IsVisible = true;
    }
}
