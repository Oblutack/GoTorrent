using Avalonia.Controls;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// A comma-separated tag list prompt - same no-ViewModel plain-code-behind
/// pattern as <see cref="SetCategoryWindow"/>. Pre-filled from the
/// selected row's own <c>Tags</c> (unlike <see cref="SetSpeedLimitsWindow"/>,
/// tags round-trip on <c>TorrentSummary</c> just like Category does, so
/// there's a real current value to show).
/// </summary>
public partial class SetTagsWindow : Window
{
    private readonly MainViewModel _mainViewModel;
    private readonly string _infoHash;

    public SetTagsWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
        _infoHash = string.Empty;
    }

    public SetTagsWindow(MainViewModel mainViewModel, string infoHash, IReadOnlyList<string>? currentTags)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        _infoHash = infoHash;
        TagsBox.Text = currentTags is { Count: > 0 } ? string.Join(", ", currentTags) : null;
    }

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnOkClick(object? sender, RoutedEventArgs e)
    {
        var tags = (TagsBox.Text ?? string.Empty)
            .Split(',', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries);

        OkButton.IsEnabled = false;
        var ok = await _mainViewModel.SetTagsAsync(_infoHash, tags);
        if (ok)
        {
            Close();
            return;
        }
        ErrorText.Text = _mainViewModel.ConnectionError ?? "Could not set tags.";
        ErrorText.IsVisible = true;
        OkButton.IsEnabled = true;
    }
}
