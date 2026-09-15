using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using Avalonia;
using Avalonia.Controls;
using Avalonia.Controls.Notifications;
using Avalonia.Input;
using Avalonia.Interactivity;
using GoTorrent.Desktop.Models;
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

    /// <summary>
    /// Renders <see cref="MainViewModel.ToastRequested"/> as an in-window
    /// overlay - Avalonia's own notification type, not a real OS toast
    /// (see <c>Services/IDesktopNotifier</c> for the one genuine OS
    /// notification this app sends). Created once <see cref="AttachViewModel"/>
    /// runs, rather than in the constructor, since <see cref="WindowNotificationManager"/>
    /// wants a real <see cref="TopLevel"/> to attach to and there's no
    /// reason to build one before a <see cref="MainViewModel"/> exists to
    /// actually feed it.
    /// </summary>
    private WindowNotificationManager? _notifications;

    /// <summary>
    /// The window's own last known <b>Normal</b>-state bounds - tracked
    /// continuously (see <see cref="OnPropertyChanged"/>) rather than
    /// read at save time, since <see cref="Window.Width"/>/
    /// <see cref="Window.Height"/>/<see cref="Window.Position"/> reflect
    /// the *maximized* bounds while <see cref="WindowState.Maximized"/>
    /// is active - saving those directly would mean every restart of a
    /// maximized app "forgets" its real restored size entirely.
    /// </summary>
    private double _lastNormalWidth;
    private double _lastNormalHeight;
    private PixelPoint _lastNormalPosition;

    /// <summary>
    /// What <see cref="ShowAtRestoredState"/> (the tray "Show" handler)
    /// should set <see cref="Window.WindowState"/> back to - without
    /// this, showing a maximized-then-minimized-to-tray window from the
    /// tray always reset it to <see cref="WindowState.Normal"/>, a real
    /// (if minor) pre-existing UX bug this same geometry work now fixes
    /// as a natural side effect of tracking Normal-vs-Maximized properly.
    /// </summary>
    private WindowState _restoredState = WindowState.Normal;

    public MainWindow()
    {
        InitializeComponent();
        Closing += OnClosing;
        PositionChanged += OnPositionChanged;
        DragDrop.AddDropHandler(this, OnDrop);
        DragDrop.AddDragOverHandler(this, OnDragOver);
        // Tunnel, not Bubble - confirmed live that Bubble alone lets the
        // DataGrid's own internal Enter handling (row navigation/commit,
        // even in a read-only grid) mark the event Handled before it
        // ever reaches this Window-level handler, so "Open Containing
        // Folder" on Enter silently never fired. Tunnel runs this first,
        // before any child control gets a chance to swallow the key -
        // the explicit "skip while a TextBox has focus" check below is
        // what keeps that safe for normal typing, not the routing phase.
        AddHandler(KeyDownEvent, OnKeyDown, RoutingStrategies.Tunnel);
    }

    public void AllowRealClose() => _reallyClose = true;

    /// <summary>
    /// Wires up everything that needs a real <see cref="MainViewModel"/>
    /// instance to exist first - called once from <c>App.axaml.cs</c>
    /// right after <see cref="Window.DataContext"/> is set, since the
    /// constructor runs before an object initializer's property
    /// assignments and so can't reach it yet.
    /// </summary>
    public void AttachViewModel(MainViewModel mainViewModel)
    {
        RestoreGeometry(mainViewModel.SavedSettings);
        _notifications = new WindowNotificationManager(this) { Position = NotificationPosition.BottomRight, MaxItems = 3 };
        mainViewModel.ToastRequested += OnToastRequested;
    }

    /// <summary>
    /// <see cref="Notification.OnClick"/> is Avalonia's own "the user
    /// clicked this notification" hook - used for the delete-with-undo
    /// toast's "Undo" affordance, since <see cref="WindowNotificationManager"/>
    /// notifications have no separate "action button" concept of their
    /// own. A toast with no <see cref="ToastMessage.Action"/> just omits
    /// the handler entirely, so clicking it does nothing but dismiss it.
    /// </summary>
    private void OnToastRequested(ToastMessage toast)
    {
        var type = toast.Severity switch
        {
            ToastSeverity.Success => NotificationType.Success,
            ToastSeverity.Warning => NotificationType.Warning,
            ToastSeverity.Error => NotificationType.Error,
            _ => NotificationType.Information,
        };
        var text = toast.ActionLabel is { } actionLabel ? $"{toast.Text}  ({actionLabel})" : toast.Text;
        var notification = new Notification("GoTorrent", text, type, onClick: toast.Action);
        _notifications?.Show(notification);
    }

    /// <summary>
    /// Applies a previously-saved <see cref="Services.DesktopSettings"/>'
    /// window geometry. A missing <see cref="Services.DesktopSettings.WindowWidth"/>/
    /// <see cref="Services.DesktopSettings.WindowHeight"/> (null - no
    /// saved geometry yet) leaves the window at its XAML-declared
    /// default size/position entirely untouched.
    /// </summary>
    private void RestoreGeometry(Services.DesktopSettings settings)
    {
        if (settings is { WindowWidth: { } width, WindowHeight: { } height })
        {
            Width = width;
            Height = height;
            _lastNormalWidth = width;
            _lastNormalHeight = height;
        }
        if (settings is { WindowX: { } x, WindowY: { } y })
        {
            Position = new PixelPoint(x, y);
            _lastNormalPosition = Position;
        }
        if (settings.WindowMaximized)
        {
            _restoredState = WindowState.Maximized;
            WindowState = WindowState.Maximized;
        }
        if (settings.DetailSplitFraction is { } fraction && DetailSplitGrid.RowDefinitions.Count == 3)
        {
            DetailSplitGrid.RowDefinitions[0] = new RowDefinition(fraction, GridUnitType.Star);
            DetailSplitGrid.RowDefinitions[2] = new RowDefinition(1 - fraction, GridUnitType.Star);
        }
    }

    /// <summary>
    /// The tray "Show" handler - <see cref="Window.Show()"/> alone
    /// doesn't undo a <see cref="WindowState.Minimized"/> left over from
    /// this app's own "minimize to tray" (see <see cref="OnPropertyChanged"/>),
    /// and always forcing <see cref="WindowState.Normal"/> here (the
    /// previous behaviour) meant a maximized window came back un-maximized
    /// every time it was hidden and reshown via the tray - restoring
    /// <see cref="_restoredState"/> instead fixes both at once.
    /// </summary>
    public void ShowAtRestoredState()
    {
        Show();
        WindowState = _restoredState;
        Activate();
    }

    /// <summary>
    /// Persists the window's current geometry - called whenever the
    /// window is about to become hidden (see <see cref="OnClosing"/> and
    /// <see cref="OnPropertyChanged"/>'s minimize-to-tray interception),
    /// never on every resize/move tick.
    /// </summary>
    private void SaveGeometry()
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }
        double? splitFraction = null;
        if (DetailSplitGrid.RowDefinitions is [{ } topRow, _, { } bottomRow] && topRow.Height.IsStar && bottomRow.Height.IsStar)
        {
            var total = topRow.Height.Value + bottomRow.Height.Value;
            if (total > 0)
            {
                splitFraction = topRow.Height.Value / total;
            }
        }
        mainViewModel.SaveWindowGeometry(_lastNormalWidth, _lastNormalHeight, _lastNormalPosition.X, _lastNormalPosition.Y, WindowState == WindowState.Maximized, splitFraction);
    }

    private void OnPositionChanged(object? sender, PixelPointEventArgs e)
    {
        if (WindowState == WindowState.Normal)
        {
            _lastNormalPosition = e.Point;
        }
    }

    private void OnClosing(object? sender, WindowClosingEventArgs e)
    {
        SaveGeometry();
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
    /// tray icon is the only way back to it while minimized. Also where
    /// <see cref="_lastNormalWidth"/>/<see cref="_lastNormalHeight"/>/
    /// <see cref="_restoredState"/> are kept up to date - tracked
    /// continuously rather than read once at save time, since by the
    /// time <see cref="SaveGeometry"/> runs here the state being saved
    /// *for* is already Minimized (about to Hide), not the Normal/
    /// Maximized state that actually needs remembering.
    /// </summary>
    protected override void OnPropertyChanged(AvaloniaPropertyChangedEventArgs change)
    {
        base.OnPropertyChanged(change);
        if (change.Property == WindowStateProperty)
        {
            if (WindowState == WindowState.Minimized)
            {
                SaveGeometry();
                Hide();
            }
            else
            {
                _restoredState = WindowState;
            }
            return;
        }
        if (WindowState != WindowState.Normal)
        {
            return;
        }
        if (change.Property == WidthProperty)
        {
            _lastNormalWidth = Width;
        }
        else if (change.Property == HeightProperty)
        {
            _lastNormalHeight = Height;
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

    /// <summary>
    /// Stage 4's keyboard shortcuts - Delete/Space/F5/Ctrl+F/Enter/Ctrl+N,
    /// none of which existed before. Deliberately guarded on "does a
    /// TextBox currently have focus" rather than trusting every key to
    /// arrive pre-marked <c>Handled</c> by whatever has focus: a plain
    /// <see cref="TextBox"/> reliably consumes Delete/Backspace (it has
    /// to, to actually delete a character), but plain Space or Enter -
    /// both real shortcuts here - have no default action in a single-line
    /// TextBox and would otherwise silently bubble up here while a user
    /// is just typing a search query. Multi-select (and so a real Ctrl+A)
    /// doesn't exist yet - see ROADMAP.md's own Stage 4 entry - so it's
    /// deliberately not wired to anything here rather than bound to a
    /// single-item "select all" that wouldn't mean anything.
    /// </summary>
    private void OnKeyDown(object? sender, KeyEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel)
        {
            return;
        }
        if (FocusManager?.GetFocusedElement() is TextBox)
        {
            return;
        }

        switch (e.Key, e.KeyModifiers)
        {
            case (Key.Delete, KeyModifiers.None):
                mainViewModel.DeleteSelectedCommand.Execute(null);
                e.Handled = true;
                break;
            case (Key.Space, KeyModifiers.None):
                TogglePauseResume(mainViewModel);
                e.Handled = true;
                break;
            case (Key.F5, KeyModifiers.None):
                _ = mainViewModel.RefreshAsync();
                e.Handled = true;
                break;
            case (Key.F, KeyModifiers.Control):
                SearchBox.Focus();
                e.Handled = true;
                break;
            case (Key.Enter, KeyModifiers.None):
                OpenContainingFolder(mainViewModel);
                e.Handled = true;
                break;
            case (Key.N, KeyModifiers.Control):
                OnAddTorrentClick(sender, e);
                e.Handled = true;
                break;
        }
    }

    private static void TogglePauseResume(MainViewModel mainViewModel)
    {
        if (mainViewModel.SelectedTorrent is not { } torrent)
        {
            return;
        }
        if (torrent.State == "Paused")
        {
            mainViewModel.ResumeSelectedCommand.Execute(null);
        }
        else
        {
            mainViewModel.PauseSelectedCommand.Execute(null);
        }
    }

    private void OnOpenContainingFolderClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is MainViewModel mainViewModel)
        {
            OpenContainingFolder(mainViewModel);
        }
    }

    /// <summary>
    /// "The single most-used context-menu item in any torrent client"
    /// per ROADMAP.md - <c>explorer.exe /select,"path"</c> both opens the
    /// folder and highlights the item, working the same way whether
    /// <see cref="Models.TorrentDetail.ContentPath"/> is a single file or
    /// a multi-file torrent's own subfolder. Needs
    /// <see cref="MainViewModel.DetailTorrent"/> (the selected torrent's
    /// already-loaded detail), not just <see cref="MainViewModel.SelectedTorrent"/> -
    /// the content path isn't part of the list-view summary. Silently
    /// does nothing if the path is missing or doesn't exist yet (a
    /// torrent that's still <c>FetchingMetadata</c>, or has downloaded
    /// nothing at all) rather than showing an error for what's a very
    /// ordinary, expected state.
    /// </summary>
    private static void OpenContainingFolder(MainViewModel mainViewModel)
    {
        var path = mainViewModel.DetailTorrent?.ContentPath;
        if (string.IsNullOrEmpty(path))
        {
            return;
        }
        try
        {
            if (File.Exists(path) || Directory.Exists(path))
            {
                Process.Start(new ProcessStartInfo("explorer.exe", $"/select,\"{path}\"") { UseShellExecute = true });
            }
        }
        catch (Exception ex) when (ex is Win32Exception or InvalidOperationException)
        {
            // Best-effort - explorer.exe missing/unreachable isn't worth a toast.
        }
    }

    private async void OnCopyMagnetClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel || mainViewModel.SelectedTorrent is not { } torrent)
        {
            return;
        }
        var magnet = $"magnet:?xt=urn:btih:{torrent.InfoHash}&dn={Uri.EscapeDataString(torrent.Name)}";
        await CopyToClipboardAsync(magnet);
    }

    private async void OnCopyInfoHashClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is MainViewModel { SelectedTorrent: { } torrent })
        {
            await CopyToClipboardAsync(torrent.InfoHash);
        }
    }

    private async void OnCopyNameClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is MainViewModel { SelectedTorrent: { } torrent })
        {
            await CopyToClipboardAsync(torrent.Name);
        }
    }

    /// <summary>
    /// Avalonia 12's real clipboard API (confirmed against this project's
    /// actual installed package, not a remembered older version - the
    /// same "check the assembly/XML docs directly" discipline 6.3's
    /// drag-and-drop rewrite already established for this exact kind of
    /// API drift): <c>IClipboard.SetTextAsync</c> no longer exists.
    /// <see cref="DataTransfer"/> is a "mutable implementation of both
    /// <c>IDataTransfer</c> and <c>IAsyncDataTransfer</c>" per its own
    /// doc comment, so it can be built with a single
    /// <see cref="DataTransferItem.CreateText"/> item and handed to
    /// <c>IClipboard.SetDataAsync</c> directly - no separate sync-to-async
    /// wrapper needed despite one existing in the framework for other cases.
    /// </summary>
    private Task CopyToClipboardAsync(string text)
    {
        var transfer = new DataTransfer();
        transfer.Add(DataTransferItem.CreateText(text));
        return Clipboard?.SetDataAsync(transfer) ?? Task.CompletedTask;
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

    private async void OnSetTagsClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel || mainViewModel.SelectedTorrent is null)
        {
            return;
        }
        var dialog = new SetTagsWindow(mainViewModel, mainViewModel.SelectedTorrent.InfoHash, mainViewModel.SelectedTorrent.Tags);
        await dialog.ShowDialog(this);
    }

    private async void OnSetSpeedLimitsClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel || mainViewModel.SelectedTorrent is null)
        {
            return;
        }
        var dialog = new SetSpeedLimitsWindow(mainViewModel, mainViewModel.SelectedTorrent.InfoHash);
        await dialog.ShowDialog(this);
    }

    private async void OnSetLocationClick(object? sender, RoutedEventArgs e)
    {
        if (DataContext is not MainViewModel mainViewModel || mainViewModel.SelectedTorrent is null)
        {
            return;
        }
        var dialog = new SetLocationWindow(mainViewModel, mainViewModel.SelectedTorrent.InfoHash, mainViewModel.DetailTorrent?.DownloadDir);
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