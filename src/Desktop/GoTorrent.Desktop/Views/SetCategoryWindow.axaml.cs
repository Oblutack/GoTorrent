using Avalonia.Controls;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// A one-field prompt for the selected torrent's category - same
/// no-ViewModel plain-code-behind pattern as <see cref="AddTorrentWindow"/>/
/// <see cref="PreferencesWindow"/>. Calls straight into
/// <see cref="MainViewModel.SetCategoryAsync"/>, which is what's actually
/// unit-tested.
/// </summary>
public partial class SetCategoryWindow : Window
{
    private readonly MainViewModel _mainViewModel;
    private readonly string _infoHash;

    public SetCategoryWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
        _infoHash = string.Empty;
    }

    public SetCategoryWindow(MainViewModel mainViewModel, string infoHash, string? currentCategory)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        _infoHash = infoHash;
        CategoryBox.Text = currentCategory;
    }

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnOkClick(object? sender, RoutedEventArgs e)
    {
        OkButton.IsEnabled = false;
        var ok = await _mainViewModel.SetCategoryAsync(_infoHash, CategoryBox.Text ?? string.Empty);
        if (ok)
        {
            Close();
            return;
        }
        ErrorText.Text = "Could not set category.";
        ErrorText.IsVisible = true;
        OkButton.IsEnabled = true;
    }
}
