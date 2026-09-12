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

    public AddTorrentWindow()
    {
        InitializeComponent();
        _mainViewModel = null!;
    }

    public AddTorrentWindow(MainViewModel mainViewModel)
    {
        InitializeComponent();
        _mainViewModel = mainViewModel;
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

    private void OnCancelClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnAddClick(object? sender, RoutedEventArgs e)
    {
        var magnet = MagnetBox.Text?.Trim();
        var url = UrlBox.Text?.Trim();
        var category = string.IsNullOrWhiteSpace(CategoryBox.Text) ? null : CategoryBox.Text!.Trim();
        var downloadDir = string.IsNullOrWhiteSpace(DownloadDirBox.Text) ? null : DownloadDirBox.Text!.Trim();

        AddButton.IsEnabled = false;
        try
        {
            bool ok;
            if (!string.IsNullOrWhiteSpace(magnet))
            {
                ok = await _mainViewModel.AddMagnetAsync(magnet, category, downloadDir);
            }
            else if (_selectedFilePath is not null)
            {
                var bytes = await File.ReadAllBytesAsync(_selectedFilePath);
                ok = await _mainViewModel.AddTorrentFileAsync(bytes, Path.GetFileName(_selectedFilePath), category, downloadDir);
            }
            else if (!string.IsNullOrWhiteSpace(url))
            {
                ok = await _mainViewModel.AddUrlAsync(url, category, downloadDir);
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
        ErrorText.Text = message;
        ErrorText.IsVisible = true;
    }
}
