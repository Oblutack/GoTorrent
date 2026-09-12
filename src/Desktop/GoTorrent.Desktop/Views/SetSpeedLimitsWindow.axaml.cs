using Avalonia.Controls;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// A per-torrent down/up limit prompt - same no-ViewModel plain-code-behind
/// pattern as <see cref="SetCategoryWindow"/>. Always starts at "0"
/// (unlimited) rather than trying to pre-fill a current value, since
/// gottrentd's <c>TorrentSummary</c>/<c>TorrentDetail</c> never report a
/// torrent's own limit back - see
/// <see cref="MainViewModel.SetSpeedLimitsAsync"/>'s own doc comment.
/// </summary>
public partial class SetSpeedLimitsWindow : Window
{
    private readonly MainViewModel _mainViewModel;
    private readonly string _infoHash;

    public SetSpeedLimitsWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
        _infoHash = string.Empty;
    }

    public SetSpeedLimitsWindow(MainViewModel mainViewModel, string infoHash)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        _infoHash = infoHash;
    }

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnOkClick(object? sender, RoutedEventArgs e)
    {
        if (!long.TryParse(DownLimitBox.Text, out var down) || down < 0)
        {
            ShowError("Download limit must be a non-negative number.");
            return;
        }
        if (!long.TryParse(UpLimitBox.Text, out var up) || up < 0)
        {
            ShowError("Upload limit must be a non-negative number.");
            return;
        }

        OkButton.IsEnabled = false;
        var ok = await _mainViewModel.SetSpeedLimitsAsync(_infoHash, down, up);
        if (ok)
        {
            Close();
            return;
        }
        ShowError(_mainViewModel.ConnectionError ?? "Could not set speed limits.");
        OkButton.IsEnabled = true;
    }

    private void ShowError(string message)
    {
        ErrorText.Text = message;
        ErrorText.IsVisible = true;
    }
}
