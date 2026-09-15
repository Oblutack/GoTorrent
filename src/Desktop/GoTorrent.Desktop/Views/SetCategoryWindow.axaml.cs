using Avalonia.Controls;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// A one-field prompt for the selected torrent(s)' category - same
/// no-ViewModel plain-code-behind pattern as <see cref="AddTorrentWindow"/>/
/// <see cref="PreferencesWindow"/>. Calls straight into
/// <see cref="MainViewModel.SetCategoryForSelectedAsync"/>, which is
/// what's actually unit-tested and operates on the ViewModel's own
/// current <c>SelectedTorrents</c> - this dialog no longer takes an
/// explicit info hash for that reason, only whatever category to
/// pre-fill the box with (null when more than one torrent is selected,
/// since their categories might differ).
/// </summary>
public partial class SetCategoryWindow : Window
{
    private readonly MainViewModel _mainViewModel;

    public SetCategoryWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
    }

    public SetCategoryWindow(MainViewModel mainViewModel, string? currentCategory)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        CategoryBox.Text = currentCategory;
    }

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnOkClick(object? sender, RoutedEventArgs e)
    {
        OkButton.IsEnabled = false;
        var ok = await _mainViewModel.SetCategoryForSelectedAsync(CategoryBox.Text ?? string.Empty);
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
