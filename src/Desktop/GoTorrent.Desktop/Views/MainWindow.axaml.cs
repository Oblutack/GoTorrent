using Avalonia;
using Avalonia.Controls;
using Avalonia.Input;
using Avalonia.Interactivity;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Views;

public partial class MainWindow : Window
{
    /// <summary>
    /// Set only by the tray icon's own "Exit" command - lets
    /// <see cref="OnClosing"/> tell a real quit apart from the user
    /// clicking the window's close button, which should hide to the
    /// tray instead (torrents keep transferring in the background
    /// either way).
    /// </summary>
    private bool _reallyClose;

    public MainWindow()
    {
        InitializeComponent();
        Closing += OnClosing;
        DragDrop.AddDropHandler(this, OnDrop);
        DragDrop.AddDragOverHandler(this, OnDragOver);
    }

    public void AllowRealClose() => _reallyClose = true;

    private void OnClosing(object? sender, WindowClosingEventArgs e)
    {
        if (_reallyClose)
        {
            return;
        }
        e.Cancel = true;
        Hide();
    }

    /// <summary>
    /// True "minimize to tray": the window disappears from the taskbar
    /// entirely instead of just collapsing to a taskbar button, so the
    /// tray icon is the only way back to it while minimized.
    /// </summary>
    protected override void OnPropertyChanged(AvaloniaPropertyChangedEventArgs change)
    {
        base.OnPropertyChanged(change);
        if (change.Property == WindowStateProperty && WindowState == WindowState.Minimized)
        {
            Hide();
        }
    }

    /// <summary>
    /// Dragging and double-click-to-maximize on the title bar row itself
    /// are handled natively by
    /// <c>chrome:WindowDecorationProperties.ElementRole="TitleBar"</c>
    /// (MainWindow.axaml) - Avalonia 12's real replacement for the older
    /// manual <c>PointerPressed</c>+<c>BeginMoveDrag</c> pattern, which no
    /// longer exists. These three handlers are only the caption buttons
    /// themselves, which the drag region correctly excludes from its own
    /// hit-testing.
    /// </summary>
    private void OnMinimizeClick(object? sender, RoutedEventArgs e) => WindowState = WindowState.Minimized;

    private void OnMaximizeRestoreClick(object? sender, RoutedEventArgs e) =>
        WindowState = WindowState == WindowState.Maximized ? WindowState.Normal : WindowState.Maximized;

    /// <summary>Goes through the normal <see cref="Window.Close"/> path - <see cref="OnClosing"/> still decides whether that's a real exit or a hide-to-tray, same as the native close button always did.</summary>
    private void OnCloseClick(object? sender, RoutedEventArgs e) => Close();

    private async void OnAddTorrentClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }
        var dialog = new AddTorrentWindow(mainViewModel);
        await dialog.ShowDialog(this);
    }

    private async void OnPreferencesClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }
        var dialog = new PreferencesWindow(mainViewModel);
        await dialog.ShowDialog(this);
    }

    private async void OnSetCategoryClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel || mainViewModel.SelectedTorrent is null)
        {
            return;
        }
        var dialog = new SetCategoryWindow(mainViewModel, mainViewModel.SelectedTorrent.InfoHash, mainViewModel.SelectedTorrent.Category);
        await dialog.ShowDialog(this);
    }

    private async void OnAddTrackerClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }
        var url = NewTrackerBox.Text?.Trim();
        if (string.IsNullOrEmpty(url))
        {
            return;
        }
        if (await mainViewModel.AddTrackerAsync(url))
        {
            NewTrackerBox.Text = string.Empty;
        }
    }

    /// <summary>
    /// Fires both for a real user pick and for the ComboBox re-syncing to
    /// its bound <see cref="Models.FileEntry.Priority"/> whenever
    /// <c>DetailFiles</c> is rebuilt (every detail-pane refresh) - only
    /// calling the API when the selection actually differs from the row's
    /// own current value tells the two apart without needing a separate
    /// "is this a real user action" flag.
    /// </summary>
    private async void OnFilePriorityChanged(object? sender, SelectionChangedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }
        if (sender is not ComboBox { DataContext: Models.FileEntry entry, SelectedItem: string selected })
        {
            return;
        }
        if (selected == entry.Priority)
        {
            return;
        }
        var index = mainViewModel.DetailFiles.IndexOf(entry);
        if (index < 0)
        {
            return;
        }
        await mainViewModel.SetFilePriorityAsync(index, selected);
    }

    /// <summary>
    /// Loads the detail pane immediately when the user picks a different
    /// row, instead of waiting out the rest of the 2s auto-refresh
    /// interval - RefreshAsync already reloads it on every tick, this
    /// just makes clicking a row feel instant too. Also kicks
    /// RefreshPeerRatesAsync once here for the same reason - it no longer
    /// runs as a side effect of LoadSelectedDetailAsync (that used to
    /// double-poll peers alongside the dedicated 1Hz timer), so without
    /// this the Peers tab would wait up to 1s after a manual selection
    /// change before showing anything for the newly selected torrent.
    /// </summary>
    private async void OnTorrentSelectionChanged(object? sender, SelectionChangedEventArgs e)
    {
        if (DataContext is MainViewModel mainViewModel)
        {
            await mainViewModel.LoadSelectedDetailAsync();
            await mainViewModel.RefreshPeerRatesAsync();
        }
    }

    /// <summary>
    /// Only a file or plain-text drag (a dropped `.torrent` from Explorer,
    /// or a `magnet:` link dragged out of a browser's address bar) shows
    /// a "you can drop this" cursor - anything else (an image, a URL
    /// dragged as a rich HTML fragment with no plain-text form) is left
    /// at the default "no drop" effect DragEventArgs already carries.
    /// </summary>
    private void OnDragOver(object? sender, DragEventArgs e)
    {
        e.DragEffects = e.DataTransfer.Formats.Contains(DataFormat.File) || e.DataTransfer.Formats.Contains(DataFormat.Text)
            ? DragDropEffects.Copy
            : DragDropEffects.None;
    }

    /// <summary>
    /// Routes every dropped item through the same
    /// <see cref="MainViewModel.AddFromArgumentAsync"/> the file-association
    /// launch path (see <c>App.axaml.cs</c>) already uses - one place
    /// decides "magnet text vs. a real file path", not a second copy of
    /// that logic here.
    /// </summary>
    private async void OnDrop(object? sender, DragEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }

        if (e.DataTransfer.TryGetFiles() is { } files)
        {
            foreach (var file in files)
            {
                await mainViewModel.AddFromArgumentAsync(file.Path.LocalPath);
            }
            return;
        }

        if (e.DataTransfer.TryGetText() is { } text && !string.IsNullOrWhiteSpace(text))
        {
            await mainViewModel.AddFromArgumentAsync(text.Trim());
        }
    }
}