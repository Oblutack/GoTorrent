using System;
using System.IO;
using System.Threading.Tasks;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Platform.Storage;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

/// <summary>
/// No ViewModel of its own - a file picker is UI chrome with nothing
/// worth unit-testing, so this window calls straight into the already
/// unit-tested <see cref="MainViewModel.AddMagnetAsync"/>/
/// <see cref="MainViewModel.AddTorrentFileAsync"/>.
/// </summary>
public partial class AddTorrentWindow : Window
{
    private readonly MainViewModel _mainViewModel;
    private string? _selectedFilePath;

    /// <summary>
    /// Set once a disk-space warning has been shown for the current Add
    /// click's file/URL/save-path combination, so a second click of Add
    /// proceeds anyway rather than showing the same warning forever - the
    /// "warn, don't block" contract this guard is meant to have. Not
    /// reset if the user changes the selection after seeing a warning
    /// (a real, accepted simplification: the next Add click just skips a
    /// re-check rather than re-warning, which is harmless either way
    /// since this is advisory only).
    /// </summary>
    private bool _diskSpaceWarningAcknowledged;

    public AddTorrentWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
    }

    public AddTorrentWindow(MainViewModel mainViewModel)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
        DownloadDirBox.ItemsSource = mainViewModel.RecentDownloadDirs;
    }

    private async void OnBrowseClick(object? sender, RoutedEventArgs e)
    {
        var topLevel = GetTopLevel(this);
        if (topLevel is null)
        {
            return;
        }

        var files = await topLevel.StorageProvider.OpenFilePickerAsync(new FilePickerOpenOptions
        {
            Title = "Select a .torrent file",
            AllowMultiple = false,
            FileTypeFilter = [new FilePickerFileType("Torrent files") { Patterns = ["*.torrent"] }],
        });

        if (files.Count > 0)
        {
            _selectedFilePath = files[0].Path.LocalPath;
            FilePathBox.Text = _selectedFilePath;
        }
    }

    private async void OnBrowseDirClick(object? sender, RoutedEventArgs e)
    {
        var topLevel = GetTopLevel(this);
        if (topLevel is null)
        {
            return;
        }

        var folders = await topLevel.StorageProvider.OpenFolderPickerAsync(new FolderPickerOpenOptions
        {
            Title = "Select a save directory",
            AllowMultiple = false,
        });

        if (folders.Count > 0)
        {
            DownloadDirBox.Text = folders[0].Path.LocalPath;
        }
    }

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnAddClick(object? sender, RoutedEventArgs e)
    {
        var magnet = MagnetBox.Text?.Trim();
        var url = UrlBox.Text?.Trim();
        var category = string.IsNullOrWhiteSpace(CategoryBox.Text) ? null : CategoryBox.Text!.Trim();
        var downloadDir = string.IsNullOrWhiteSpace(DownloadDirBox.Text) ? null : DownloadDirBox.Text!.Trim();
        var tags = string.IsNullOrWhiteSpace(TagsBox.Text)
            ? null
            : TagsBox.Text!.Split(',', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries);
        var sequential = SequentialCheckBox.IsChecked == true;

        AddButton.IsEnabled = false;
        try
        {
            // Stage 6's disk-space guard - only meaningful for a real
            // file/URL add with an explicit save path: a magnet has no
            // known size until peers supply metadata (no preview route
            // exists for one on the Go side either), and with no save
            // path typed, the app has no way to know what gottrentd's
            // own default/category path would resolve to.
            if (!_diskSpaceWarningAcknowledged)
            {
                string? warning = null;
                if (_selectedFilePath is not null)
                {
                    var previewBytes = await File.ReadAllBytesAsync(_selectedFilePath);
                    warning = await _mainViewModel.CheckDiskSpaceForFileAsync(previewBytes, Path.GetFileName(_selectedFilePath), downloadDir);
                }
                else if (!string.IsNullOrWhiteSpace(url))
                {
                    warning = await _mainViewModel.CheckDiskSpaceForUrlAsync(url, downloadDir);
                }
                if (warning is not null)
                {
                    ShowWarning(warning + " Click Add again to add it anyway.");
                    _diskSpaceWarningAcknowledged = true;
                    return;
                }
            }
            _diskSpaceWarningAcknowledged = false;
            WarningText.IsVisible = false;

            bool ok;
            if (!string.IsNullOrWhiteSpace(magnet))
            {
                ok = await _mainViewModel.AddMagnetAsync(magnet, category, downloadDir, tags, sequential);
            }
            else if (_selectedFilePath is not null)
            {
                var bytes = await File.ReadAllBytesAsync(_selectedFilePath);
                ok = await _mainViewModel.AddTorrentFileAsync(bytes, Path.GetFileName(_selectedFilePath), category, downloadDir, tags, sequential);
            }
            else if (!string.IsNullOrWhiteSpace(url))
            {
                ok = await _mainViewModel.AddUrlAsync(url, category, downloadDir, tags, sequential);
            }
            else
            {
                ShowError("Enter a magnet link, a .torrent URL, or choose a .torrent file.");
                return;
            }

            if (ok)
            {
                Close();
            }
            else
            {
                ShowError(_mainViewModel.AddTorrentError ?? "Failed to add torrent.");
            }
        }
        finally
        {
            AddButton.IsEnabled = true;
        }
    }

    private void ShowError(string message)
    {
        WarningText.IsVisible = false;
        ErrorText.Text = message;
        ErrorText.IsVisible = true;
    }

    private void ShowWarning(string message)
    {
        ErrorText.IsVisible = false;
        WarningText.Text = message;
        WarningText.IsVisible = true;
    }
}
