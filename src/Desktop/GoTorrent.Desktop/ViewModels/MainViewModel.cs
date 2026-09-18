using System.Collections.ObjectModel;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Linq;
using Avalonia;
using Avalonia.Styling;
using Avalonia.Threading;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.ViewModels;

public partial class MainViewModel : ViewModelBase, IDisposable
{
    /// <summary>How many speed-graph samples to keep - one sessionStats WS message arrives per second, so this is a rolling five-minute window.</summary>
    private const int MaxSpeedSamples = 300;

    private readonly Func<EngineOptions, IEngineClient> _clientFactory;
    private readonly ISettingsStore _settingsStore;
    private readonly IEventStream _eventStream;
    private readonly TimeProvider _timeProvider;
    private readonly IAutostartService _autostartService;
    private readonly IFileAssociationService _fileAssociationService;
    private readonly IDaemonLauncher _daemonLauncher;
    private readonly IDesktopNotifier _desktopNotifier;
    private readonly IUpdateChecker _updateChecker;
    private readonly HashSet<string> _notifiedCompletionHashes = [];
    private readonly HashSet<string> _pendingDeleteHashes = [];
    private readonly Dictionary<string, CancellationTokenSource> _pendingDeleteCancellations = [];
    private IEngineClient? _client;
    private EngineOptions? _connectedOptions;
    private DispatcherTimer? _timer;
    private DispatcherTimer? _peerTimer;
    private bool _liveEventsStarted;
    private DateTimeOffset? _lastSpeedSampleTime;
    private long _lastTotalDownloaded;
    private long _lastTotalUploaded;
    private string? _peerRatesForHash;
    private DateTimeOffset? _lastPeerSampleTime;
    private Dictionary<string, (long Downloaded, long Uploaded)> _lastPeerTotals = [];
    private string? _pieceOwnersForHash;
    private CancellationTokenSource? _detailLoadCts;
    private bool _autoRefreshInFlight;
    private bool _peerRefreshInFlight;
    private readonly CancellationTokenSource _lifetimeCts = new();

    /// <summary>
    /// One stable <see cref="TorrentRowViewModel"/> per torrent gottrentd
    /// reports, for as long as it's managed - never reassigned wholesale
    /// and never has its existing rows replaced, only updated in place
    /// (<see cref="ReconcileTorrents"/>). See <see cref="TorrentRowViewModel"/>'s
    /// own doc comment for the real selection bug this fixes.
    /// </summary>
    [ObservableProperty]
    public partial ObservableCollection<TorrentRowViewModel> Torrents { get; set; } = [];

    /// <summary>The sidebar's own view of <see cref="Torrents"/> - status filter, then category, then <see cref="SearchText"/>, recomputed by <see cref="ApplyFilter"/> whenever any of those change. Reconciled in place (<see cref="SyncCollection{T}"/>), same reasoning as <see cref="Torrents"/> itself.</summary>
    [ObservableProperty]
    public partial ObservableCollection<TorrentRowViewModel> DisplayedTorrents { get; set; } = [];

    /// <summary>Built-in status filters plus one entry per distinct category actually present - recomputed alongside <see cref="DisplayedTorrents"/>, so a category that no torrent uses anymore disappears on its own.</summary>
    [ObservableProperty]
    public partial ObservableCollection<SidebarFilter> SidebarFilters { get; set; } = [];

    [ObservableProperty]
    public partial SidebarFilter SelectedFilter { get; set; } = new(SidebarFilter.AllKey, "All");

    [ObservableProperty]
    public partial string SearchText { get; set; } = string.Empty;

    /// <summary>
    /// Stage 3's empty-state overlays for the torrent list - two
    /// distinct messages for two distinct reasons the grid could be
    /// empty ("you have no torrents at all" vs. "your filter/search
    /// matched none of the torrents you do have"), recomputed alongside
    /// <see cref="DisplayedTorrents"/> in <see cref="ApplyFilter"/> since
    /// XAML has no clean way to express "these two collections' counts,
    /// compared" as a binding condition on its own.
    /// </summary>
    [ObservableProperty]
    public partial bool ShowEmptyFleetMessage { get; set; }

    [ObservableProperty]
    public partial bool ShowNoMatchesMessage { get; set; }

    /// <summary>
    /// <see cref="SessionRatioDisplay"/> is derived from this and has to be
    /// notified explicitly (same <c>[NotifyPropertyChangedFor]</c> pattern
    /// as <see cref="RowHeight"/>/<see cref="CompactDensity"/>) since
    /// CommunityToolkit.Mvvm can't infer a computed property's own
    /// dependency on its own.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(SessionRatioDisplay))]
    public partial SessionStats? Session { get; set; }

    /// <summary>
    /// The Statistics window's share-ratio line. A plain string, not a
    /// double + XAML <c>StringFormat</c>, specifically so the "nothing
    /// downloaded yet but something was uploaded" case (an initial seed)
    /// can render "∞" instead of a bare division producing
    /// <see cref="double.PositiveInfinity"/> and a StringFormat rendering
    /// that as the word "Infinity".
    /// </summary>
    public string SessionRatioDisplay => Session switch
    {
        { TotalDownloaded: > 0 } s => ((double)s.TotalUploaded / s.TotalDownloaded).ToString("F2", CultureInfo.InvariantCulture),
        { TotalUploaded: > 0 } => "∞",
        _ => "0.00",
    };

    /// <summary>
    /// When this Desktop last successfully connected to the currently-
    /// attached gottrentd, for the Statistics window's "session uptime"
    /// line - set once, the first time <see cref="TryConnect"/> succeeds
    /// in this process run, and never reset by a later reconnect (a brief
    /// drop-and-recover shouldn't restart the clock). This is genuinely
    /// "how long this Desktop instance has been talking to a daemon," not
    /// gottrentd's own process uptime - the control API has no uptime
    /// field of its own to read.
    /// </summary>
    public DateTimeOffset? ConnectedSince { get; private set; }

    [ObservableProperty]
    public partial bool IsConnected { get; set; }

    [ObservableProperty]
    public partial string? ConnectionError { get; set; }

    [ObservableProperty]
    public partial string BaseAddressInput { get; set; } = "http://127.0.0.1:6880/";

    [ObservableProperty]
    public partial string TokenInput { get; set; } = string.Empty;

    [ObservableProperty]
    public partial TorrentRowViewModel? SelectedTorrent { get; set; }

    /// <summary>
    /// Stage 4's multi-select - every torrent currently selected in the
    /// grid, kept in sync from <c>MainWindow.OnTorrentSelectionChanged</c>
    /// reading the DataGrid's own <c>SelectedItems</c> (a get-only list,
    /// not something Avalonia's DataGrid exposes as a bindable property -
    /// confirmed against the installed package's XML docs rather than
    /// assumed, so this is synced imperatively from code-behind instead
    /// of a XAML binding). <see cref="SelectedTorrent"/> keeps meaning
    /// "the one torrent the detail pane shows" (the grid's own concept of
    /// the primary/anchor selection) even when this has more than one
    /// entry - the General/Files/Peers/Trackers tabs only ever show one
    /// torrent's detail regardless of how many rows are selected.
    /// </summary>
    [ObservableProperty]
    public partial ObservableCollection<TorrentRowViewModel> SelectedTorrents { get; set; } = [];

    [ObservableProperty]
    public partial string? AddTorrentError { get; set; }

    [ObservableProperty]
    public partial TorrentDetail? DetailTorrent { get; set; }

    [ObservableProperty]
    public partial ObservableCollection<FileEntry> DetailFiles { get; set; } = [];

    /// <summary>The Files tab's priority ComboBox options - gottrentd's own lowercase priority names (picker.Priority.MarshalText), never changes so it's not observable.</summary>
    public static IReadOnlyList<string> FilePriorityOptions { get; } = ["skip", "low", "normal", "high"];

    [ObservableProperty]
    public partial ObservableCollection<PeerRow> DetailPeers { get; set; } = [];

    [ObservableProperty]
    public partial ObservableCollection<TrackerEntry> DetailTrackers { get; set; } = [];

    [ObservableProperty]
    public partial ObservableCollection<bool> PieceHave { get; set; } = [];

    /// <summary>
    /// Parallel to <see cref="PieceHave"/> - the peer address that
    /// delivered each piece, or null when unknown (a piece already
    /// verified before this torrent was selected/this session connected -
    /// gottrentd's <c>pieceVerified</c> event only carries an owner going
    /// forward, there's no way to ask "who delivered piece N" after the
    /// fact). Reset to all-null alongside <see cref="PieceHave"/> on every
    /// selection change; filled in live as real <c>pieceVerified</c>
    /// events arrive for the selected torrent (see <see cref="HandleEvent"/>).
    /// </summary>
    [ObservableProperty]
    public partial ObservableCollection<string?> PieceOwners { get; set; } = [];

    /// <summary>
    /// Stage 6's "why is this slow?" diagnostics panel - see
    /// <see cref="RecomputeDiagnosisAsync"/> for what each message means
    /// and where it comes from. Empty when nothing is selected or
    /// nothing noteworthy was found for a torrent that isn't
    /// <c>Downloading</c>/<c>Paused</c>/<c>FetchingMetadata</c>/
    /// <c>CheckingFiles</c> (a <c>Seeding</c> or <c>Error</c> torrent has
    /// nothing this panel is meant to diagnose).
    /// </summary>
    [ObservableProperty]
    public partial IReadOnlyList<string> DiagnosisMessages { get; set; } = [];

    [ObservableProperty]
    public partial ObservableCollection<double> DownloadRateHistory { get; set; } = [];

    [ObservableProperty]
    public partial ObservableCollection<double> UploadRateHistory { get; set; } = [];

    [ObservableProperty]
    public partial double LatestDownloadRateKBps { get; set; }

    [ObservableProperty]
    public partial double LatestUploadRateKBps { get; set; }

    [ObservableProperty]
    public partial bool StartMinimized { get; set; }

    [ObservableProperty]
    public partial bool AutostartEnabled { get; set; }

    [ObservableProperty]
    public partial bool FileAssociationEnabled { get; set; }

    [ObservableProperty]
    public partial bool DaemonStarting { get; set; }

    [ObservableProperty]
    public partial bool LightTheme { get; set; }

    /// <summary>
    /// Stage 3's density toggle. <see cref="RowHeight"/> is what the four
    /// torrent-related <c>DataGrid</c>s (Torrents/Files/Peers/Trackers)
    /// actually bind to - <c>[NotifyPropertyChangedFor]</c> is what makes
    /// changing this bool also notify that derived property, since
    /// CommunityToolkit.Mvvm doesn't infer a computed property's
    /// dependencies on its own.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(RowHeight))]
    public partial bool CompactDensity { get; set; }

    /// <summary>NaN lets a DataGrid fall back to its own default (comfortable) row height; a real value overrides it for compact mode.</summary>
    public double RowHeight => CompactDensity ? CompactRowHeight : double.NaN;

    private const double CompactRowHeight = 24;

    /// <summary>
    /// Stage 4's column chooser - one bool per optional torrent-list
    /// column (Name/State/Progress stay always visible), each backing a
    /// <c>CheckBox</c> in a toolbar flyout and persisted via
    /// <see cref="DesktopSettings.HiddenColumns"/>. Plain properties
    /// rather than a keyed collection - simpler to bind directly from
    /// XAML (<c>$parent[Window].DataContext.ShowXxxColumn</c>, the same
    /// idiom the Files tab's priority <c>ComboBox</c> already uses to
    /// reach the Window's DataContext from inside a DataGrid cell
    /// template) than indexing into a collection would be.
    /// </summary>
    [ObservableProperty]
    public partial bool ShowSizeColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowCategoryColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowTagsColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowDownloadedColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowUploadedColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowRatioColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowPeersColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowQueueColumn { get; set; } = true;

    /// <summary>
    /// Down/Up rate and ETA columns - Stage 6's "per-torrent speed and
    /// ETA, computed client-side" (see <see cref="TorrentRowViewModel"/>'s
    /// own doc comments on <c>DownloadRateKBps</c>/<c>EtaDisplay</c> for
    /// how). Shown by default like every other optional column.
    /// </summary>
    [ObservableProperty]
    public partial bool ShowDownRateColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowUpRateColumn { get; set; } = true;

    [ObservableProperty]
    public partial bool ShowEtaColumn { get; set; } = true;

    partial void OnShowDownRateColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowUpRateColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowEtaColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowSizeColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowCategoryColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowTagsColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowDownloadedColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowUploadedColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowRatioColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowPeersColumnChanged(bool value) => SaveHiddenColumns();

    partial void OnShowQueueColumnChanged(bool value) => SaveHiddenColumns();

    /// <summary>
    /// The Add Torrent dialog's "recently used" save-path suggestions -
    /// most-recent first, capped at <see cref="MaxRecentDownloadDirs"/>.
    /// Populated from <see cref="DesktopSettings.RecentDownloadDirs"/> at
    /// construction and appended to by <see cref="RecordRecentDownloadDir"/>
    /// after any successful add that named an explicit save path.
    /// </summary>
    [ObservableProperty]
    public partial ObservableCollection<string> RecentDownloadDirs { get; set; } = [];

    private const int MaxRecentDownloadDirs = 8;

    /// <summary>
    /// Moves <paramref name="downloadDir"/> to the front of
    /// <see cref="RecentDownloadDirs"/> (de-duplicating a re-used path
    /// rather than listing it twice) and persists the result. A no-op for
    /// a blank path - "used gottrentd's default" isn't a real path worth
    /// remembering.
    /// </summary>
    private void RecordRecentDownloadDir(string? downloadDir)
    {
        if (string.IsNullOrWhiteSpace(downloadDir))
        {
            return;
        }
        var updated = new List<string> { downloadDir };
        updated.AddRange(RecentDownloadDirs.Where(d => d != downloadDir));
        if (updated.Count > MaxRecentDownloadDirs)
        {
            updated.RemoveRange(MaxRecentDownloadDirs, updated.Count - MaxRecentDownloadDirs);
        }
        RecentDownloadDirs = new ObservableCollection<string>(updated);
        _settingsStore.Save(_settingsStore.Load() with { RecentDownloadDirs = updated });
    }

    /// <summary>Empties the Add Torrent dialog's recent-save-path suggestions. Used by the Preferences dialog's Downloads section.</summary>
    public void ClearRecentDownloadDirs()
    {
        RecentDownloadDirs = [];
        _settingsStore.Save(_settingsStore.Load() with { RecentDownloadDirs = [] });
    }

    /// <summary>
    /// Re-derives the flat hidden-columns list from the 8 bools above and
    /// persists it - called from every one of their <c>OnXxxChanged</c>
    /// hooks, so a single `CheckBox` toggle in the flyout saves
    /// immediately rather than needing a separate "Save" action the
    /// column chooser (deliberately a lightweight flyout, not a dialog)
    /// has no natural place for.
    /// </summary>
    private void SaveHiddenColumns()
    {
        var hidden = new List<string>();
        if (!ShowSizeColumn)
        {
            hidden.Add("Size");
        }
        if (!ShowCategoryColumn)
        {
            hidden.Add("Category");
        }
        if (!ShowTagsColumn)
        {
            hidden.Add("Tags");
        }
        if (!ShowDownloadedColumn)
        {
            hidden.Add("Downloaded");
        }
        if (!ShowUploadedColumn)
        {
            hidden.Add("Uploaded");
        }
        if (!ShowRatioColumn)
        {
            hidden.Add("Ratio");
        }
        if (!ShowPeersColumn)
        {
            hidden.Add("Peers");
        }
        if (!ShowQueueColumn)
        {
            hidden.Add("Queue");
        }
        if (!ShowDownRateColumn)
        {
            hidden.Add("DownRate");
        }
        if (!ShowUpRateColumn)
        {
            hidden.Add("UpRate");
        }
        if (!ShowEtaColumn)
        {
            hidden.Add("Eta");
        }
        _settingsStore.Save(_settingsStore.Load() with { HiddenColumns = hidden });
    }

    public MainViewModel() : this(options => new EngineClient(options), new FileSettingsStore(), new WebSocketEventStream(), TimeProvider.System, new WindowsAutostartService(), new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier(), new GitHubUpdateChecker())
    {
    }

    /// <summary>
    /// The <paramref name="clientFactory"/>/<paramref name="settingsStore"/>
    /// seams are what make this testable without a real gottrentd or
    /// real file I/O - tests pass a fake client factory and an
    /// in-memory settings store.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore)
        : this(clientFactory, settingsStore, new WebSocketEventStream(), TimeProvider.System, new WindowsAutostartService(), new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier(), new GitHubUpdateChecker())
    {
    }

    /// <summary>
    /// <paramref name="eventStream"/>/<paramref name="timeProvider"/> are
    /// the same kind of testability seam - a fake event stream so tests
    /// never open a real socket, and a fake clock so
    /// <see cref="HandleEvent"/>'s speed-sample-rate math doesn't depend
    /// on real wall-clock time elapsing between two calls in a test.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider)
        : this(clientFactory, settingsStore, eventStream, timeProvider, new WindowsAutostartService(), new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier(), new GitHubUpdateChecker())
    {
    }

    /// <summary>
    /// <paramref name="autostartService"/> is the same kind of seam again -
    /// tests use a fake so "is GoTorrent registered to launch at login"
    /// never depends on (or mutates) the real Windows registry.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier(), new GitHubUpdateChecker())
    {
    }

    /// <summary>
    /// <paramref name="fileAssociationService"/> - same seam again, for
    /// the same reason as <paramref name="autostartService"/>.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, fileAssociationService, new DaemonLauncher(), new WindowsDesktopNotifier(), new GitHubUpdateChecker())
    {
    }

    /// <summary>
    /// <paramref name="daemonLauncher"/> - same seam again: tests use a
    /// fake so "spawn gottrentd" never starts a real process.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService, IDaemonLauncher daemonLauncher)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, fileAssociationService, daemonLauncher, new WindowsDesktopNotifier(), new GitHubUpdateChecker())
    {
    }

    /// <summary>
    /// <paramref name="desktopNotifier"/> - same seam again: tests use a
    /// fake so "notify on completion" never shows a real OS notification.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService, IDaemonLauncher daemonLauncher, IDesktopNotifier desktopNotifier)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, fileAssociationService, daemonLauncher, desktopNotifier, new GitHubUpdateChecker())
    {
    }

    /// <summary>
    /// <paramref name="updateChecker"/> - same seam again: tests script
    /// whether a newer release "exists" without a real call to GitHub.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService, IDaemonLauncher daemonLauncher, IDesktopNotifier desktopNotifier, IUpdateChecker updateChecker)
    {
        _clientFactory = clientFactory;
        _settingsStore = settingsStore;
        _eventStream = eventStream;
        _timeProvider = timeProvider;
        _autostartService = autostartService;
        _fileAssociationService = fileAssociationService;
        _daemonLauncher = daemonLauncher;
        _desktopNotifier = desktopNotifier;
        _updateChecker = updateChecker;

        var settings = _settingsStore.Load();
        SavedSettings = settings;
        StartMinimized = settings.StartMinimized;
        LightTheme = settings.LightTheme;
        CompactDensity = settings.CompactDensity;
        var hiddenColumns = settings.HiddenColumns ?? [];
        ShowSizeColumn = !hiddenColumns.Contains("Size");
        ShowCategoryColumn = !hiddenColumns.Contains("Category");
        ShowTagsColumn = !hiddenColumns.Contains("Tags");
        ShowDownloadedColumn = !hiddenColumns.Contains("Downloaded");
        ShowUploadedColumn = !hiddenColumns.Contains("Uploaded");
        ShowRatioColumn = !hiddenColumns.Contains("Ratio");
        ShowPeersColumn = !hiddenColumns.Contains("Peers");
        ShowQueueColumn = !hiddenColumns.Contains("Queue");
        ShowDownRateColumn = !hiddenColumns.Contains("DownRate");
        ShowUpRateColumn = !hiddenColumns.Contains("UpRate");
        ShowEtaColumn = !hiddenColumns.Contains("Eta");
        RecentDownloadDirs = new ObservableCollection<string>(settings.RecentDownloadDirs ?? []);
        AutostartEnabled = _autostartService.IsEnabled();
        FileAssociationEnabled = _fileAssociationService.IsRegistered();
        if (settings.IsConfigured)
        {
            BaseAddressInput = settings.BaseAddress!;
            // Fire-and-forget - a constructor can't be async, and this
            // matches every other "kick off real work, don't block
            // construction on it" seam in this class (StartAutoRefresh/
            // StartLiveEvents/CheckForUpdatesAsync are all the same shape).
            _ = TryConnectAsync(settings.BaseAddress!, settings.Token!, persist: false);
        }
    }

    /// <summary>Whether a local gottrentd binary was found next to this app - gates the connect screen's "Start gottrentd" button.</summary>
    public bool DaemonAvailable => _daemonLauncher.IsAvailable;

    /// <summary>Whether this instance spawned the daemon currently running - gates the tray "Exit" flow's offer to stop it too.</summary>
    public bool WeOwnRunningDaemon => _daemonLauncher.IsRunning;

    /// <summary>Stops the daemon this instance spawned, if any. Used by <c>App.axaml.cs</c>'s tray "Exit" handler.</summary>
    public void StopLocalDaemon() => _daemonLauncher.Stop();

    /// <summary>Wires up real OS notifications on completion. Called once from <c>App.axaml.cs</c>, once the main window's real native handle exists.</summary>
    public void AttachDesktopNotifier(IntPtr ownerWindowHandle) => _desktopNotifier.Attach(ownerWindowHandle);

    /// <summary>
    /// Transient, action-scoped feedback ("Category set", "Tracker
    /// added", "Couldn't pause: …") - <c>MainWindow</c> renders these
    /// through Avalonia's <c>WindowNotificationManager</c>. Deliberately
    /// separate from <see cref="ConnectionError"/>, which stays reserved
    /// for the persistent "we've lost connection to gottrentd" case -
    /// before this event existed, every per-action catch block wrote
    /// into <see cref="ConnectionError"/> too, so a single failed Pause
    /// left the connection-lost banner permanently lit until the next
    /// unrelated success happened to clear it.
    /// </summary>
    public event Action<ToastMessage>? ToastRequested;

    private void Toast(string text, ToastSeverity severity, string? actionLabel = null, Action? action = null) =>
        ToastRequested?.Invoke(new ToastMessage(text, severity, actionLabel, action));

    /// <summary>
    /// Stage 6's "quick-add from clipboard" - the value most recently
    /// offered via <see cref="OfferClipboardMagnetIfNew"/>, so re-
    /// activating the window with the same magnet still on the clipboard
    /// (the common case: copy once, alt-tab back and forth while
    /// deciding whether to add it) doesn't re-show the same toast every
    /// single time the window regains focus.
    /// </summary>
    private string? _lastOfferedClipboardMagnet;

    /// <summary>
    /// Called from <c>MainWindow</c>'s real <c>Activated</c> event
    /// handler with whatever text (if any) is currently on the OS
    /// clipboard - reading the clipboard itself is real platform I/O
    /// that belongs in the View, this is the testable logic on top of
    /// it. Offers a one-click "Add" toast only for something that looks
    /// like a magnet link, and only once per distinct value.
    /// </summary>
    public void OfferClipboardMagnetIfNew(string? clipboardText)
    {
        if (clipboardText is null || !clipboardText.StartsWith("magnet:", StringComparison.OrdinalIgnoreCase))
        {
            return;
        }
        if (clipboardText == _lastOfferedClipboardMagnet)
        {
            return;
        }
        _lastOfferedClipboardMagnet = clipboardText;
        Toast("Magnet link found on clipboard.", ToastSeverity.Info, "Add", () => _ = AddMagnetAsync(clipboardText, category: null, downloadDir: null));
    }

    /// <summary>
    /// Called once at startup, fire-and-forget from <c>App.axaml.cs</c> -
    /// a single check, not a recurring loop, so it doesn't get a
    /// <c>StartXxx</c> name the way <see cref="StartAutoRefresh"/>/
    /// <see cref="StartLiveEvents"/>/<see cref="StartPeerRefresh"/> do.
    /// Compares GitHub's latest release tag against this build's own
    /// <see cref="AppVersion.Current"/> and raises an actionable toast
    /// (matching the clipboard-magnet toast's own shape) if a newer one
    /// exists. <see cref="IUpdateChecker"/>'s own contract guarantees it
    /// never throws and returns null for "nothing to report" - a failed
    /// check is silently that, never a surfaced error, since this is a
    /// purely advisory background check.
    /// </summary>
    public async Task CheckForUpdatesAsync()
    {
        var tag = await _updateChecker.GetLatestVersionTagAsync(CancellationToken.None);
        if (tag is null)
        {
            return;
        }
        var normalizedTag = tag.TrimStart('v', 'V');
        if (!Version.TryParse(normalizedTag, out var latest) || !Version.TryParse(AppVersion.Current, out var current) || latest <= current)
        {
            return;
        }
        Toast($"A new version ({tag}) is available.", ToastSeverity.Info, "View", OpenLatestReleasePage);
    }

    private static void OpenLatestReleasePage()
    {
        try
        {
            Process.Start(new ProcessStartInfo("https://github.com/Oblutack/GoTorrent/releases/latest") { UseShellExecute = true });
        }
        catch
        {
            // Best effort - nothing more this app can usefully do if
            // launching the OS's own browser handler fails.
        }
    }

    /// <summary>
    /// How long <see cref="DeleteSelectedAsync"/> waits before actually
    /// calling <c>DeleteAsync</c> - a public settable property rather
    /// than another constructor-injected seam (this codebase's usual
    /// pattern for real dependencies) since it's a plain duration, not a
    /// service: tests shrink it to keep the suite fast, production uses
    /// a real human-reaction-time window.
    /// </summary>
    public TimeSpan UndoDeleteDelay { get; set; } = TimeSpan.FromSeconds(6);

    [RelayCommand]
    private Task ConnectAsync() => TryConnectAsync(BaseAddressInput, TokenInput, persist: true);

    /// <summary>
    /// Probes the real API (<see cref="TryReachAsync"/>) before declaring
    /// success - this used to just construct a client object and set
    /// <see cref="IsConnected"/> true unconditionally, with no API call at
    /// all, so a wrong token or an address nothing is listening on landed
    /// the user in the main UI with an error banner on the very next
    /// refresh, rather than a clear "couldn't connect" failure right here
    /// at the connect screen where it's actually actionable.
    /// </summary>
    private async Task TryConnectAsync(string baseAddress, string token, bool persist)
    {
        Uri baseUri;
        try
        {
            baseUri = new Uri(baseAddress);
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
            IsConnected = false;
            return;
        }

        if (!await TryReachAsync(baseUri, token))
        {
            ConnectionError = "Could not reach gottrentd at this address with this token - check both are correct and the daemon is running.";
            IsConnected = false;
            return;
        }

        var options = new EngineOptions(baseUri, token);
        var newClient = _clientFactory(options);
        // Reconnecting (a second Connect click, or daemon supervision
        // attaching after a spawn) used to just overwrite _client, leaking
        // the previous one's real HttpClient/socket handles.
        (_client as IDisposable)?.Dispose();
        _client = newClient;
        _connectedOptions = options;
        IsConnected = true;
        ConnectionError = null;
        ConnectedSince ??= _timeProvider.GetUtcNow();
        if (persist)
        {
            // `with` rather than a fresh DesktopSettings - this must not
            // clobber StartMinimized (or any other future preference) back
            // to its default every time the user hits Connect.
            _settingsStore.Save(_settingsStore.Load() with { BaseAddress = baseAddress, Token = token });
        }
    }

    /// <summary>
    /// Daemon supervision's "attach if running, spawn if not": first tries
    /// <see cref="BaseAddressInput"/> with whatever token
    /// <see cref="IDaemonLauncher.TryReadExistingToken"/> finds, and only
    /// spawns a fresh gottrentd if that fails or no token file exists yet.
    /// <see cref="TryConnectAsync"/> already probes the real API itself
    /// now (see its own doc comment), so checking <see cref="IsConnected"/>
    /// after calling it is enough to tell "attach worked" apart from
    /// "need to spawn instead" - this used to make its own separate
    /// <see cref="TryReachAsync"/> call first for exactly that answer,
    /// which duplicated the probe <see cref="TryConnectAsync"/> now
    /// always does anyway.
    /// </summary>
    [RelayCommand]
    private async Task StartLocalDaemonAsync()
    {
        if (DaemonStarting)
        {
            return;
        }
        DaemonStarting = true;
        ConnectionError = null;
        try
        {
            Uri baseUri;
            try
            {
                baseUri = new Uri(BaseAddressInput);
            }
            catch (Exception ex)
            {
                ConnectionError = ex.Message;
                return;
            }

            var existingToken = _daemonLauncher.TryReadExistingToken();
            if (existingToken is not null)
            {
                await TryConnectAsync(BaseAddressInput, existingToken, persist: true);
                if (IsConnected)
                {
                    return;
                }
            }

            var token = await _daemonLauncher.StartAsync(baseUri.Authority, CancellationToken.None);
            if (token is null)
            {
                ConnectionError = "Could not start gottrentd - it may already be running on a different address, or the executable could not be found next to this app.";
                return;
            }
            await TryConnectAsync(BaseAddressInput, token, persist: true);
        }
        finally
        {
            DaemonStarting = false;
        }
    }

    /// <summary>A real API call, not just constructing a client - proves gottrentd is actually reachable at <paramref name="baseAddress"/>, not just that a token file happens to exist.</summary>
    private async Task<bool> TryReachAsync(Uri baseAddress, string token)
    {
        var probe = _clientFactory(new EngineOptions(baseAddress, token));
        try
        {
            await probe.GetSessionAsync(CancellationToken.None);
            return true;
        }
        catch
        {
            return false;
        }
        finally
        {
            // This one's always a throwaway, whether the probe succeeds or
            // not - a real connection is TryConnect's own separate client.
            (probe as IDisposable)?.Dispose();
        }
    }

    /// <summary>
    /// Persists the "start minimized to tray" preference. Used by the
    /// preferences dialog's code-behind, same reasoning as
    /// <see cref="GetSessionLimitsAsync"/>/<see cref="SetSessionLimitsAsync"/>.
    /// </summary>
    public void SetStartMinimized(bool value)
    {
        StartMinimized = value;
        _settingsStore.Save(_settingsStore.Load() with { StartMinimized = value });
    }

    /// <summary>
    /// Persists the light/dark theme preference and flips it live via
    /// <see cref="Application.RequestedThemeVariant"/> - App.axaml's
    /// ThemeDictionaries (the GtXxx tokens, referenced everywhere as
    /// <c>{DynamicResource ...}</c>, never <c>{StaticResource ...}</c>)
    /// react to that change immediately, no restart needed. Guarded with
    /// <c>Application.Current is { }</c> rather than <c>Current!</c> so
    /// this stays callable from a ViewModel-only test with no real
    /// Avalonia <see cref="Application"/> running - the setting still
    /// persists and <see cref="LightTheme"/> still updates either way,
    /// just the visual half is a no-op outside a real app.
    /// </summary>
    public void SetLightTheme(bool value)
    {
        LightTheme = value;
        _settingsStore.Save(_settingsStore.Load() with { LightTheme = value });
        if (Application.Current is { } app)
        {
            app.RequestedThemeVariant = value ? ThemeVariant.Light : ThemeVariant.Dark;
        }
    }

    /// <summary>Persists the comfortable/compact row-density preference - see <see cref="RowHeight"/>.</summary>
    public void SetCompactDensity(bool value)
    {
        CompactDensity = value;
        _settingsStore.Save(_settingsStore.Load() with { CompactDensity = value });
    }

    /// <summary>
    /// The window geometry <c>MainWindow</c> should restore itself to at
    /// startup - read once, here, rather than <c>MainWindow</c> owning
    /// an <see cref="ISettingsStore"/> of its own. Refreshed after every
    /// <see cref="SaveWindowGeometry"/> call so a caller reading it back
    /// (there isn't one today, but the same "re-read after every write"
    /// shape as <see cref="StartMinimized"/> above) sees the latest value.
    /// </summary>
    public DesktopSettings SavedSettings { get; private set; }

    /// <summary>
    /// Persists window size/position/maximized-state and the detail-pane
    /// splitter position - called from <c>MainWindow</c> whenever it's
    /// about to become hidden (minimized to tray or a real close), never
    /// on every resize/move tick. <paramref name="width"/>/
    /// <paramref name="height"/>/<paramref name="x"/>/<paramref name="y"/>
    /// are the window's last known <b>Normal</b>-state bounds - see
    /// <c>MainWindow.SaveGeometry</c> for why maximized bounds are never
    /// the ones saved here.
    /// </summary>
    public void SaveWindowGeometry(double width, double height, int x, int y, bool maximized, double? detailSplitFraction)
    {
        SavedSettings = _settingsStore.Load() with
        {
            WindowWidth = width,
            WindowHeight = height,
            WindowX = x,
            WindowY = y,
            WindowMaximized = maximized,
            DetailSplitFraction = detailSplitFraction,
        };
        _settingsStore.Save(SavedSettings);
    }

    /// <summary>
    /// Registers or unregisters this app to launch at login. Nothing is
    /// persisted in <see cref="DesktopSettings"/> for this one - the
    /// Windows registry itself is the source of truth, so a user removing
    /// it outside the app (or on another machine's settings.json) is
    /// reflected correctly rather than fought.
    /// </summary>
    public void SetAutostart(bool value)
    {
        _autostartService.SetEnabled(value);
        AutostartEnabled = value;
    }

    /// <summary>
    /// Registers or unregisters this app as the handler for
    /// <c>.torrent</c> files and <c>magnet:</c> links. Same
    /// registry-is-the-source-of-truth reasoning as
    /// <see cref="AutostartEnabled"/> - nothing persisted here either.
    /// </summary>
    public void SetFileAssociation(bool value)
    {
        _fileAssociationService.SetRegistered(value);
        FileAssociationEnabled = value;
    }

    /// <summary>
    /// Adds whatever the OS handed this process on the command line -
    /// a <c>magnet:</c> URI (double-clicked a magnet link, once
    /// <see cref="IFileAssociationService"/> is registered) or a
    /// <c>.torrent</c> file path (double-clicked the file itself).
    /// Called once, from <c>App.axaml.cs</c>, right after startup -
    /// there is no retry if <see cref="_client"/> isn't connected yet,
    /// since a fresh launch's auto-connect (see the constructor) has
    /// already run by the time this is called.
    /// </summary>
    public async Task AddFromArgumentAsync(string argument)
    {
        if (_client is null)
        {
            AddTorrentError = $"Not connected to gottrentd - could not add \"{argument}\".";
            return;
        }
        if (argument.StartsWith("magnet:", StringComparison.OrdinalIgnoreCase))
        {
            await AddMagnetAsync(argument, category: null, downloadDir: null);
        }
        else if (File.Exists(argument))
        {
            var bytes = await File.ReadAllBytesAsync(argument);
            await AddTorrentFileAsync(bytes, Path.GetFileName(argument), category: null, downloadDir: null);
        }
        else
        {
            AddTorrentError = $"Don't know how to add \"{argument}\".";
        }
    }

    public async Task RefreshAsync()
    {
        if (_client is null)
        {
            return;
        }
        try
        {
            var torrents = await _client.ListTorrentsAsync(CancellationToken.None);
            ReconcileTorrents(torrents);
            ApplyFilter();
            Session = await _client.GetSessionAsync(CancellationToken.None);
            ConnectionError = null;
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
        }
        await LoadSelectedDetailAsync();
    }

    /// <summary>
    /// Updates <see cref="Torrents"/> from a fresh poll without ever
    /// replacing a still-present row's object - existing rows (matched by
    /// <see cref="TorrentRowViewModel.InfoHash"/>) are updated in place via
    /// <see cref="TorrentRowViewModel.UpdateFrom"/>, new ones get a new
    /// <see cref="TorrentRowViewModel"/>, and rows no longer reported are
    /// dropped. This is what makes <see cref="SelectedTorrent"/> (and the
    /// DataGrid selection it's bound to) survive a refresh automatically -
    /// see <see cref="TorrentRowViewModel"/>'s own doc comment for why that
    /// used to silently break.
    ///
    /// <para>
    /// Explicitly nulls <see cref="SelectedTorrent"/> when its row is gone
    /// (deleted, or gottrentd simply stopped reporting it) rather than
    /// counting on the real <c>DataGrid</c>'s own two-way-binding
    /// side effect to do it - that side effect is real and does fire in
    /// the actual running app, but a ViewModel that only behaves correctly
    /// through a specific View's binding quirks isn't actually correct on
    /// its own, and a plain ViewModel-level unit test (no real
    /// <c>SelectingItemsControl</c> in the loop) proved it: caught by this
    /// method's own test, not by inspection.
    /// </para>
    /// </summary>
    private void ReconcileTorrents(IReadOnlyList<TorrentSummary> fresh)
    {
        var existingByHash = Torrents.ToDictionary(t => t.InfoHash);
        var updatedOrder = new List<TorrentRowViewModel>(fresh.Count);
        foreach (var summary in fresh)
        {
            if (existingByHash.TryGetValue(summary.InfoHash, out var row))
            {
                row.UpdateFrom(summary);
            }
            else
            {
                row = new TorrentRowViewModel(summary, _timeProvider);
            }
            updatedOrder.Add(row);
        }
        SyncCollection(Torrents, updatedOrder);
        if (SelectedTorrent is { } selected && !Torrents.Contains(selected))
        {
            SelectedTorrent = null;
        }
    }

    /// <summary>
    /// Reconciles <paramref name="collection"/> to contain exactly
    /// <paramref name="desiredOrder"/>, in that order - by reference
    /// identity for a class (e.g. <see cref="TorrentRowViewModel"/>, where
    /// that's the whole point) or by value for a <c>record</c> (e.g.
    /// <see cref="Models.SidebarFilter"/>, where <c>IndexOf</c>'s default
    /// value-equality naturally finds and keeps the existing instance for
    /// an unchanged entry rather than inserting the freshly-constructed
    /// one <paramref name="desiredOrder"/> happens to carry). Never fully
    /// clears the collection unless every single entry actually changed -
    /// the real fix for the stack-overflow/NullReferenceException class of
    /// bug a full <c>Clear()</c> used to cause here (see git history/
    /// CLAUDE.md): a bound <c>SelectingItemsControl</c>'s selection
    /// machinery reacts to the collection transiently going empty, not
    /// just to what it ends up containing.
    /// </summary>
    private static void SyncCollection<T>(ObservableCollection<T> collection, IReadOnlyList<T> desiredOrder)
    {
        for (var i = collection.Count - 1; i >= 0; i--)
        {
            if (!desiredOrder.Contains(collection[i]))
            {
                collection.RemoveAt(i);
            }
        }
        for (var i = 0; i < desiredOrder.Count; i++)
        {
            var item = desiredOrder[i];
            var currentIndex = collection.IndexOf(item);
            if (currentIndex < 0)
            {
                collection.Insert(i, item);
            }
            else if (currentIndex != i)
            {
                collection.Move(currentIndex, i);
            }
        }
    }

    partial void OnSelectedFilterChanged(SidebarFilter value) => ApplyFilter();

    partial void OnSearchTextChanged(string value) => ApplyFilter();

    private static readonly SidebarFilter AllFilter = new(SidebarFilter.AllKey, "All");
    private static readonly SidebarFilter DownloadingFilter = new(SidebarFilter.DownloadingKey, "Downloading");
    private static readonly SidebarFilter SeedingFilter = new(SidebarFilter.SeedingKey, "Seeding");
    private static readonly SidebarFilter PausedFilter = new(SidebarFilter.PausedKey, "Paused");
    private static readonly SidebarFilter ErrorFilter = new(SidebarFilter.ErrorKey, "Error");

    /// <summary>
    /// Recomputes both <see cref="SidebarFilters"/> (the built-in status
    /// filters plus one per distinct category actually present in
    /// <see cref="Torrents"/>) and <see cref="DisplayedTorrents"/> (that
    /// set, further narrowed by <see cref="SelectedFilter"/> then
    /// <see cref="SearchText"/>) - called after every refresh and whenever
    /// either of those two change.
    ///
    /// <para>
    /// Both collections are reconciled in place via <see cref="SyncCollection{T}"/>
    /// rather than <c>Clear()</c>-ed and rebuilt - the 6.5 fix for two real
    /// bugs caught live, not by a unit test, while this method still used
    /// <c>Clear()</c>/re-<c>Add()</c>: (1) reassigning <see cref="SidebarFilters"/>
    /// to a brand-new collection instance made the sidebar <c>ListBox</c>
    /// re-initialize its selection model, which - since <c>SelectedItem</c>
    /// is two-way bound to <see cref="SelectedFilter"/> - wrote back into
    /// it, re-entering this method and reassigning again, forever (a real
    /// stack overflow); (2) even after switching to in-place mutation,
    /// <c>SidebarFilters.Clear()</c> alone still left the ListBox with zero
    /// items for one moment, which made it report "nothing selected" and
    /// write a genuine <c>null</c> into <see cref="SelectedFilter"/> (its
    /// non-nullable C# annotation is compile-time only; Avalonia's binding
    /// layer does not honor it) before the rebuild finished - a real
    /// NullReferenceException. <see cref="SyncCollection{T}"/> never
    /// removes and re-adds an entry that's still wanted, so the ListBox's
    /// <c>ItemsSource</c> never transiently empties out at all for the
    /// common case (built-ins plus unchanged categories), and
    /// <see cref="SelectedFilter"/>/<see cref="SelectedTorrent"/> simply
    /// keep pointing at the same still-present object - no capture-before/
    /// restore-after dance needed anymore.
    /// </para>
    /// </summary>
    private void ApplyFilter()
    {
        // A torrent mid-way through the delete-with-undo window (see
        // DeleteSelectedAsync) stays in Torrents - the server still
        // reports it, and ReconcileTorrents has no reason to remove it -
        // but is excluded from every view of the list here, so it reads
        // as genuinely gone (categories, sidebar counts, the grid
        // itself) for as long as the deletion is still pending or
        // undoable, rather than flickering back in on the very next poll.
        var visible = _pendingDeleteHashes.Count == 0
            ? (IReadOnlyList<TorrentRowViewModel>)Torrents
            : Torrents.Where(t => !_pendingDeleteHashes.Contains(t.InfoHash)).ToList();

        var categories = visible
            .Select(t => t.Category)
            .Where(c => !string.IsNullOrWhiteSpace(c))
            .Distinct()
            .OrderBy(c => c, StringComparer.OrdinalIgnoreCase)
            .Select(c => SidebarFilter.Category(c!));

        var tags = visible
            .SelectMany(t => t.Tags ?? [])
            .Where(t => !string.IsNullOrWhiteSpace(t))
            .Distinct()
            .OrderBy(t => t, StringComparer.OrdinalIgnoreCase)
            .Select(SidebarFilter.Tag);

        var filters = new List<SidebarFilter> { AllFilter, DownloadingFilter, SeedingFilter, PausedFilter, ErrorFilter };
        filters.AddRange(categories);
        filters.AddRange(tags);
        SyncCollection(SidebarFilters, filters);

        // Every entry's Count is recomputed against the just-reconciled
        // live SidebarFilters (not the possibly-discarded freshly-built
        // filters list above - SyncCollection may have kept the *existing*
        // instance for an unchanged category/tag rather than this one, see
        // SyncCollection's own doc comment), so a mutation here always
        // lands on the object actually bound in the sidebar.
        foreach (var filter in SidebarFilters)
        {
            filter.Count = visible.Count(t => filter.Matches(t.State, t.Category, t.Tags));
        }

        // Avalonia's TextBox/ListBox can still hand back a genuine null
        // through the same two-way-binding-writes-null-during-a-transient-
        // state mechanism described above (e.g. the search box being
        // cleared entirely, or the selected filter having just been
        // removed from SidebarFilters because its category disappeared) -
        // fall back to "All" rather than trust the possibly-null live
        // property, same defensive reasoning as before, just narrower now.
        // SidebarFilters.Contains uses SidebarFilter's own value equality,
        // so for the common "nothing changed" case this finds and reuses
        // the exact reference already stored in the field - the assignment
        // below is then a true no-op, both by reference and (via
        // CommunityToolkit's generated equality-skip) by not re-raising
        // PropertyChanged/re-entering this method through OnSelectedFilterChanged.
        var selectedFilter = SelectedFilter is { } sf && SidebarFilters.Contains(sf) ? sf : AllFilter;
        SelectedFilter = selectedFilter;

        var search = (SearchText ?? string.Empty).Trim();
        var filtered = visible.Where(t => selectedFilter.Matches(t.State, t.Category, t.Tags));
        if (search.Length > 0)
        {
            filtered = filtered.Where(t => t.Name.Contains(search, StringComparison.OrdinalIgnoreCase));
        }
        SyncCollection(DisplayedTorrents, filtered.ToList());

        ShowEmptyFleetMessage = visible.Count == 0;
        ShowNoMatchesMessage = visible.Count > 0 && DisplayedTorrents.Count == 0;
    }

    /// <summary>
    /// Selects a torrent regardless of the current sidebar filter/search
    /// text - the command palette's "jump to torrent" entries call this
    /// rather than setting <see cref="SelectedTorrent"/> directly, since a
    /// torrent excluded by the live filter (a different status, or a
    /// search query that no longer matches its name) would otherwise not
    /// actually appear selected in the DataGrid at all: it simply isn't in
    /// <see cref="DisplayedTorrents"/>, the grid's own bound collection.
    /// Clearing both first (via <see cref="SearchText"/>/
    /// <see cref="SelectedFilter"/>, whose setters already run
    /// <see cref="ApplyFilter"/> synchronously) guarantees the torrent is
    /// back in view before it's actually selected.
    /// </summary>
    public void JumpToTorrent(TorrentRowViewModel torrent)
    {
        SearchText = string.Empty;
        SelectedFilter = SidebarFilters.Count > 0 ? SidebarFilters[0] : new SidebarFilter(SidebarFilter.AllKey, "All");
        SelectedTorrent = torrent;
    }

    /// <summary>
    /// Fetches the detail pane's four tabs for whatever torrent is
    /// currently selected. Called after every auto-refresh tick (so the
    /// detail pane stays live while a torrent is selected) and directly
    /// from the View when the user picks a different row, for an instant
    /// update instead of waiting out the rest of the 2s interval.
    /// Clears the detail pane rather than erroring when nothing is
    /// selected - that's a normal state, not a failure.
    ///
    /// <para>
    /// Cancels its own previous in-flight call before starting a new one:
    /// selecting torrent A (slow to respond) then quickly torrent B (fast)
    /// used to let A's four awaits resolve after B's already had, silently
    /// overwriting the detail pane with the wrong torrent's files/trackers/
    /// pieces. Every request this method makes now carries the same
    /// per-call token, so a superseded call's requests are actually
    /// cancelled (not just "whose result wins the race"), and a resulting
    /// <see cref="OperationCanceledException"/> is treated as "nothing to
    /// do," not a real failure to surface as <see cref="ConnectionError"/>.
    /// </para>
    /// </summary>
    public async Task LoadSelectedDetailAsync()
    {
        _detailLoadCts?.Cancel();
        var cts = new CancellationTokenSource();
        _detailLoadCts = cts;
        var token = cts.Token;

        if (_client is null || SelectedTorrent is null)
        {
            DetailTorrent = null;
            DetailFiles = [];
            DetailPeers = [];
            DetailTrackers = [];
            PieceHave = [];
            PieceOwners = [];
            _pieceOwnersForHash = null;
            DiagnosisMessages = [];
            return;
        }
        var hash = SelectedTorrent.InfoHash;
        try
        {
            var detail = await _client.GetTorrentDetailAsync(hash, token);
            var files = await _client.GetFilesAsync(hash, token);
            var trackers = await _client.GetTrackersAsync(hash, token);
            var pieces = await _client.GetPiecesAsync(hash, token);
            var have = pieces.ToHaveArray();
            DetailTorrent = detail;
            DetailFiles = new ObservableCollection<FileEntry>(files);
            DetailTrackers = new ObservableCollection<TrackerEntry>(trackers);
            PieceHave = new ObservableCollection<bool>(have);
            // Only start PieceOwners fresh on an actual selection change -
            // rebuilding it every 2s refresh of the SAME torrent would
            // throw away attribution HandleEvent already accumulated live
            // for pieces verified between polls (the bitfield itself is
            // idempotent across polls, but "who delivered it" is only ever
            // known from the live pieceVerified event, never re-derivable
            // from a later poll - see the property's own doc comment).
            if (hash != _pieceOwnersForHash || PieceOwners.Count != have.Length)
            {
                _pieceOwnersForHash = hash;
                PieceOwners = new ObservableCollection<string?>(new string?[have.Length]);
            }
            await RecomputeDiagnosisAsync(detail, trackers, token);
        }
        catch (OperationCanceledException)
        {
            // Superseded by a newer selection/refresh - not a real failure.
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
        }
    }

    /// <summary>
    /// Stage 6's "why is this slow?" diagnostics panel - every symptom
    /// checked here is already in gottrentd's real API (Go-side Stage 5
    /// added the one genuinely missing piece, <c>QueueHeld</c>), nobody
    /// had ever surfaced it as a single answer before. Recomputed every
    /// time <see cref="LoadSelectedDetailAsync"/> runs (every 2s while a
    /// torrent is selected, same cadence as everything else in the detail
    /// pane) rather than behind a separate manual "diagnose" button, so
    /// it stays live like the rest of this pane instead of needing to be
    /// re-triggered by hand. <paramref name="trackers"/> and
    /// <paramref name="detail"/> are the ones this same call already just
    /// fetched, not re-fetched here; peer choke state comes from
    /// <see cref="DetailPeers"/>, kept live by the separate 1Hz peer
    /// refresh - reusing it here rather than a second peer fetch means
    /// this can be at most ~1s stale, harmless for an advisory message.
    /// </summary>
    private async Task RecomputeDiagnosisAsync(TorrentDetail detail, IReadOnlyList<TrackerEntry> trackers, CancellationToken cancellationToken)
    {
        var messages = new List<string>();

        switch (detail.State)
        {
            case "Paused":
                messages.Add(detail.QueueHeld
                    ? "Paused: waiting in the queue for a slot to free up."
                    : "Paused.");
                break;
            case "FetchingMetadata":
                messages.Add("Fetching metadata from peers - needs at least one peer that already has it.");
                break;
            case "CheckingFiles":
                messages.Add("Verifying existing data on disk.");
                break;
            case "Downloading":
                if (detail.PeerCount == 0)
                {
                    messages.Add("No peers connected.");
                }
                else if (detail.SeedCount == 0)
                {
                    messages.Add("No seeds connected - some pieces may be unavailable until one joins.");
                }
                else if (DetailPeers.Count > 0 && DetailPeers.All(p => p.PeerChoking))
                {
                    messages.Add("Every connected peer is choking this download right now.");
                }
                if (trackers.Count > 0 && trackers.All(t => !string.IsNullOrEmpty(t.LastError)))
                {
                    messages.Add("Every tracker is failing - relying on DHT/PEX/LSD for peers, if enabled.");
                }
                if (_client is not null)
                {
                    try
                    {
                        var limits = await _client.GetSessionLimitsAsync(cancellationToken);
                        if (limits.DownLimitKB > 0)
                        {
                            messages.Add($"Fleet-wide download speed is capped at {limits.DownLimitKB} KiB/s.");
                        }
                    }
                    catch
                    {
                        // Advisory only - a failed limits lookup shouldn't
                        // blank out whatever else was already found.
                    }
                }
                if (messages.Count == 0)
                {
                    messages.Add("No obvious cause found - this should be downloading normally.");
                }
                break;
        }

        DiagnosisMessages = messages;
    }

    /// <summary>
    /// Polls the selected torrent's peers and turns each one's cumulative
    /// Downloaded/Uploaded into a KiB/s "contribution" rate - gottrentd's
    /// <c>GET .../peers</c> reports running totals per peer, not a rate,
    /// and there is no WS message that streams per-peer stats the way
    /// <c>sessionStats</c> streams fleet-wide ones, so this has to poll
    /// and diff itself. Runs at 1 Hz via <see cref="StartPeerRefresh"/> -
    /// the view's own <c>OnTorrentSelectionChanged</c> also calls this
    /// once immediately on a manual selection change, for the same
    /// instant-feedback reason it calls <see cref="LoadSelectedDetailAsync"/>
    /// directly rather than waiting out the rest of an interval. Not
    /// called from <see cref="LoadSelectedDetailAsync"/> itself anymore -
    /// that used to mean peers were polled from both the 2s auto-refresh
    /// tick and this method's own 1s timer at once, roughly 1.5x more
    /// often than intended.
    /// </summary>
    public async Task RefreshPeerRatesAsync()
    {
        if (_client is null || SelectedTorrent is null)
        {
            DetailPeers = [];
            _peerRatesForHash = null;
            _lastPeerTotals = [];
            _lastPeerSampleTime = null;
            return;
        }

        var hash = SelectedTorrent.InfoHash;
        if (hash != _peerRatesForHash)
        {
            // A different torrent than the last poll - a stale peer's
            // totals from that torrent would produce a nonsense delta
            // against this one's, so start this torrent's series fresh.
            _peerRatesForHash = hash;
            _lastPeerTotals = [];
            _lastPeerSampleTime = null;
        }

        IReadOnlyList<PeerEntry> peers;
        try
        {
            peers = await _client.GetPeersAsync(hash, CancellationToken.None);
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
            return;
        }

        var now = _timeProvider.GetUtcNow();
        var elapsedSeconds = _lastPeerSampleTime is { } last ? (now - last).TotalSeconds : 0;
        var rows = new List<PeerRow>(peers.Count);
        var newTotals = new Dictionary<string, (long Downloaded, long Uploaded)>(peers.Count);
        foreach (var peer in peers)
        {
            double downKBps = 0, upKBps = 0;
            if (elapsedSeconds > 0 && _lastPeerTotals.TryGetValue(peer.Addr, out var previous))
            {
                downKBps = Math.Max(0, (peer.Downloaded - previous.Downloaded) / elapsedSeconds / 1024.0);
                upKBps = Math.Max(0, (peer.Uploaded - previous.Uploaded) / elapsedSeconds / 1024.0);
            }
            rows.Add(new PeerRow(peer.Addr, peer.Outbound, downKBps, upKBps, peer.Progress, peer.AmChoking, peer.PeerChoking, peer.AmInterested, peer.PeerInterested));
            newTotals[peer.Addr] = (peer.Downloaded, peer.Uploaded);
        }

        _lastPeerTotals = newTotals;
        _lastPeerSampleTime = now;
        DetailPeers = new ObservableCollection<PeerRow>(rows);
    }

    /// <summary>
    /// Starts the live WebSocket event loop - only ever called once, from
    /// <c>App.axaml.cs</c> after a real <c>Application</c>/<c>Dispatcher</c>
    /// exists, same convention as <see cref="StartAutoRefresh"/>. Never
    /// called from tests, which call <see cref="HandleEvent"/> directly
    /// instead of driving it through a real (or fake) socket loop.
    /// </summary>
    public void StartLiveEvents()
    {
        if (_liveEventsStarted)
        {
            return;
        }
        _liveEventsStarted = true;
        _ = LiveEventLoopAsync();
    }

    /// <summary>
    /// Runs for the lifetime of the app: connects the event stream
    /// whenever this view model is connected to a gottrentd, and quietly
    /// retries after a real connection drops or a connect attempt fails
    /// (gottrentd not reachable yet, a network blip) - a dead event
    /// stream should never surface as an error the way a failed
    /// user-initiated action does, since the 2s REST poll already keeps
    /// the app usable without it.
    /// </summary>
    private async Task LiveEventLoopAsync()
    {
        var token = _lifetimeCts.Token;
        while (!token.IsCancellationRequested)
        {
            if (_connectedOptions is { } options)
            {
                try
                {
                    await foreach (var ev in _eventStream.ConnectAsync(options, token))
                    {
                        var captured = ev;
                        await Dispatcher.UIThread.InvokeAsync(() => HandleEvent(captured));
                    }
                }
                catch (OperationCanceledException)
                {
                    break;
                }
                catch
                {
                    // Connection dropped or gottrentd unreachable - retry below.
                }
            }
            try
            {
                // Not yet connected at all has nothing worth retrying
                // quickly for - a real dropped connection gets the
                // shorter delay so reconnecting still feels prompt.
                var delay = _connectedOptions is null ? TimeSpan.FromSeconds(5) : TimeSpan.FromSeconds(2);
                await Task.Delay(delay, token);
            }
            catch (OperationCanceledException)
            {
                break;
            }
        }
    }

    /// <summary>
    /// Applies one live event - public and synchronous so
    /// <c>MainViewModelTests</c> can drive it directly without a real (or
    /// fake) socket loop in the way. Must run on the UI thread when called
    /// from <see cref="LiveEventLoopAsync"/>, since it mutates bound
    /// <see cref="ObservableCollection{T}"/>s.
    /// </summary>
    public void HandleEvent(WsEvent ev)
    {
        switch (ev.Kind)
        {
            case "sessionStats" when ev.Session is not null:
                RecordSpeedSample(ev.Session);
                break;
            case "pieceVerified" when ev.PieceIndex is { } index && SelectedTorrent is not null && ev.InfoHash == SelectedTorrent.InfoHash:
                if (index >= 0 && index < PieceHave.Count)
                {
                    PieceHave[index] = true;
                }
                if (index >= 0 && index < PieceOwners.Count && !string.IsNullOrEmpty(ev.PeerAddr))
                {
                    PieceOwners[index] = ev.PeerAddr;
                }
                break;
            case "torrentStateChanged" when ev.State == "Seeding" && ev.InfoHash is { } hash:
                NotifyCompletionOnce(hash);
                break;
        }
    }

    /// <summary>
    /// A live WS <c>torrentStateChanged</c>-to-<c>Seeding</c> event only
    /// ever arrives for a genuine transition (the stream never replays
    /// past events - a torrent already Seeding when this app connects
    /// produces no event at all), so this is already "just finished" in
    /// the common case. <see cref="_notifiedCompletionHashes"/> exists as
    /// cheap insurance against the one real edge case that isn't: pausing
    /// an already-seeding torrent and resuming it re-enters
    /// <c>Seeding</c> and would otherwise re-fire this - the same
    /// "finished once, not every re-entry" distinction the Go engine's
    /// own <c>completionHookFired</c> guards against for <c>OnComplete</c>
    /// server-side (see CLAUDE.md's engine section). Per-session only -
    /// intentionally not persisted, so a fresh app launch can notify
    /// again for a torrent that finishes after a restart.
    /// </summary>
    private void NotifyCompletionOnce(string infoHash)
    {
        if (!_notifiedCompletionHashes.Add(infoHash))
        {
            return;
        }
        var name = Torrents.FirstOrDefault(t => t.InfoHash == infoHash)?.Name ?? infoHash;
        _desktopNotifier.Notify("Download complete", name);
    }

    /// <summary>
    /// Turns two consecutive sessionStats totals into a KiB/s rate for the
    /// speed graph - gottrentd streams a running total, not an
    /// instantaneous rate, so the rate is this client's own delta over
    /// elapsed real time between messages (normally ~1s apart, since
    /// that's the WS stream's own tick, but never assumed exactly 1s).
    /// </summary>
    private void RecordSpeedSample(SessionStats session)
    {
        var now = _timeProvider.GetUtcNow();
        if (_lastSpeedSampleTime is { } last)
        {
            var elapsedSeconds = (now - last).TotalSeconds;
            if (elapsedSeconds > 0)
            {
                var downKBps = Math.Max(0, (session.TotalDownloaded - _lastTotalDownloaded) / elapsedSeconds / 1024.0);
                var upKBps = Math.Max(0, (session.TotalUploaded - _lastTotalUploaded) / elapsedSeconds / 1024.0);
                AppendSample(DownloadRateHistory, downKBps);
                AppendSample(UploadRateHistory, upKBps);
                LatestDownloadRateKBps = downKBps;
                LatestUploadRateKBps = upKBps;
            }
        }
        _lastSpeedSampleTime = now;
        _lastTotalDownloaded = session.TotalDownloaded;
        _lastTotalUploaded = session.TotalUploaded;
    }

    private static void AppendSample(ObservableCollection<double> history, double value)
    {
        history.Add(value);
        while (history.Count > MaxSpeedSamples)
        {
            history.RemoveAt(0);
        }
    }

    /// <summary>
    /// Reflects intent in the grid immediately rather than waiting up to
    /// 2s for the next poll to show it (Stage 3's "optimistic UI" item) -
    /// <see cref="TorrentRowViewModel.State"/> is set right away, and
    /// <see cref="RunTorrentActionAsync"/>'s own next <see cref="RefreshAsync"/>
    /// call (on success) or the following timer tick (on failure, or if
    /// gottrentd actually did something other than what was guessed)
    /// reconciles it to the real value either way, so a wrong guess never
    /// stays wrong for more than one poll interval.
    /// </summary>
    [RelayCommand]
    private Task PauseSelectedAsync()
    {
        foreach (var torrent in SelectedTorrents)
        {
            torrent.State = "Paused";
        }
        return RunTorrentActionAsync((client, torrent) => client.PauseAsync(torrent.InfoHash, CancellationToken.None), "pause");
    }

    [RelayCommand]
    private Task ResumeSelectedAsync()
    {
        // Unlike Pause, the real next state (Downloading/Seeding/
        // CheckingFiles) isn't something this client can guess correctly
        // - "Downloading" is the closest of the three to "resumed and
        // doing something" for a torrent that isn't already complete,
        // and gets corrected to Seeding by the same reconciliation
        // PauseSelectedAsync relies on if it's wrong.
        foreach (var torrent in SelectedTorrents)
        {
            if (torrent.State == "Paused")
            {
                torrent.State = "Downloading";
            }
        }
        return RunTorrentActionAsync((client, torrent) => client.ResumeAsync(torrent.InfoHash, CancellationToken.None), "resume");
    }

    /// <summary>
    /// Plain remove (not remove-with-data) gets Stage 3's undo affordance -
    /// the actual <c>DeleteAsync</c> call is deferred by
    /// <see cref="UndoDeleteDelay"/> rather than fired immediately, so
    /// clicking "Undo" on the resulting toast can genuinely cancel it
    /// before anything destructive happens server-side, instead of having
    /// to re-add the torrent afterward. The torrent disappears from every
    /// view of the list right away regardless (<see cref="ApplyFilter"/>'s
    /// <c>_pendingDeleteHashes</c> exclusion) - waiting for the real
    /// delete to complete before hiding it would defeat the point of an
    /// undo window.
    /// </summary>
    [RelayCommand]
    private void DeleteSelected()
    {
        var torrents = SelectedTorrents.ToList();
        if (torrents.Count == 0)
        {
            return;
        }
        var hashes = torrents.Select(t => t.InfoHash).ToList();
        foreach (var hash in hashes)
        {
            _pendingDeleteHashes.Add(hash);
        }
        ApplyFilter();

        var cts = new CancellationTokenSource();
        foreach (var hash in hashes)
        {
            _pendingDeleteCancellations[hash] = cts;
        }
        var label = torrents.Count == 1 ? $"\"{torrents[0].Name}\" removed" : $"{torrents.Count} torrents removed";
        Toast(label, ToastSeverity.Info, "Undo", () => UndoDelete(hashes));
        _ = CommitPendingDeleteAsync(hashes, cts.Token);
    }

    private async Task CommitPendingDeleteAsync(IReadOnlyList<string> hashes, CancellationToken cancellationToken)
    {
        try
        {
            await Task.Delay(UndoDeleteDelay, cancellationToken);
        }
        catch (OperationCanceledException)
        {
            // Undo cancelled this exact delay - nothing to commit.
            return;
        }
        foreach (var hash in hashes)
        {
            _pendingDeleteCancellations.Remove(hash);
        }
        if (_client is null)
        {
            foreach (var hash in hashes)
            {
                _pendingDeleteHashes.Remove(hash);
            }
            ApplyFilter();
            return;
        }
        var client = _client;
        var failures = 0;
        foreach (var hash in hashes)
        {
            try
            {
                await client.DeleteAsync(hash, deleteData: false, CancellationToken.None);
            }
            catch (Exception)
            {
                // Never actually removed server-side for this one -
                // ApplyFilter below restores it to view rather than
                // leaving it stuck hidden.
                failures++;
            }
            _pendingDeleteHashes.Remove(hash);
        }
        await RefreshAsync();
        ApplyFilter();
        if (failures > 0)
        {
            Toast(failures == hashes.Count ? "Couldn't remove torrent(s)" : $"Couldn't remove {failures} of {hashes.Count} torrents", ToastSeverity.Error);
        }
    }

    private void UndoDelete(IReadOnlyList<string> hashes)
    {
        foreach (var hash in hashes)
        {
            if (_pendingDeleteCancellations.Remove(hash, out var cts))
            {
                cts.Cancel();
                cts.Dispose();
            }
            _pendingDeleteHashes.Remove(hash);
        }
        ApplyFilter();
    }

    [RelayCommand]
    private Task DeleteSelectedWithDataAsync() => RunTorrentActionAsync((client, torrent) => client.DeleteAsync(torrent.InfoHash, deleteData: true, CancellationToken.None), "delete with data");

    [RelayCommand]
    private Task VerifySelectedAsync() => RunTorrentActionAsync((client, torrent) => client.VerifyAsync(torrent.InfoHash, CancellationToken.None), "force recheck");

    [RelayCommand]
    private Task ReannounceSelectedAsync() => RunTorrentActionAsync((client, torrent) => client.ReannounceAsync(torrent.InfoHash, CancellationToken.None), "reannounce");

    [RelayCommand]
    private Task ToggleForceStartAsync() => RunTorrentActionAsync((client, torrent) =>
        client.PatchTorrentAsync(torrent.InfoHash, new PatchTorrentOptions(ForceStart: !torrent.ForceStart), CancellationToken.None), "toggle force start");

    [RelayCommand]
    private Task MoveQueueTopAsync() => RunTorrentActionAsync((client, torrent) =>
        client.PatchTorrentAsync(torrent.InfoHash, new PatchTorrentOptions(QueuePosition: 0), CancellationToken.None), "move to top");

    [RelayCommand]
    private Task MoveQueueUpAsync() => RunTorrentActionAsync((client, torrent) =>
        client.PatchTorrentAsync(torrent.InfoHash, new PatchTorrentOptions(QueuePosition: Math.Max(0, torrent.QueuePosition - 1)), CancellationToken.None), "move up");

    [RelayCommand]
    private Task MoveQueueDownAsync() => RunTorrentActionAsync((client, torrent) =>
        client.PatchTorrentAsync(torrent.InfoHash, new PatchTorrentOptions(QueuePosition: torrent.QueuePosition + 1), CancellationToken.None), "move down");

    /// <summary>
    /// gottrentd doesn't report a torrent's current picker strategy
    /// anywhere in <see cref="Models.TorrentSummary"/>/<see cref="Models.TorrentDetail"/>
    /// (it's actor-internal state, not part of either DTO), so there's
    /// nothing to toggle against - two explicit commands instead of one
    /// blind toggle.
    /// </summary>
    [RelayCommand]
    private Task EnableSequentialAsync() => RunTorrentActionAsync((client, torrent) =>
        client.PatchTorrentAsync(torrent.InfoHash, new PatchTorrentOptions(Sequential: true), CancellationToken.None), "enable sequential download");

    [RelayCommand]
    private Task DisableSequentialAsync() => RunTorrentActionAsync((client, torrent) =>
        client.PatchTorrentAsync(torrent.InfoHash, new PatchTorrentOptions(Sequential: false), CancellationToken.None), "disable sequential download");

    /// <summary>
    /// Sets a category on every currently-selected torrent - Stage 4's
    /// "set-category across a selection," the one bulk action ROADMAP.md
    /// names explicitly by itself. Used by the "Set Category..." context
    /// menu prompt's code-behind (no ViewModel of its own, same reasoning
    /// as every other small dialog). Applying the same value to every
    /// selected torrent is a straightforward, unambiguous bulk semantic -
    /// unlike Pause/Resume/Delete there's no per-torrent state to read
    /// first, just one value written everywhere.
    /// </summary>
    public async Task<bool> SetCategoryForSelectedAsync(string category)
    {
        if (_client is null || SelectedTorrents.Count == 0)
        {
            return false;
        }
        var client = _client;
        var torrents = SelectedTorrents.ToList();
        var failures = 0;
        foreach (var torrent in torrents)
        {
            try
            {
                await client.PatchTorrentAsync(torrent.InfoHash, new PatchTorrentOptions(Category: category), CancellationToken.None);
            }
            catch (Exception)
            {
                failures++;
            }
        }
        // See RunTorrentActionAsync's own comment - only refresh when at
        // least one torrent's category was actually set.
        if (failures < torrents.Count)
        {
            await RefreshAsync();
        }
        if (failures == 0)
        {
            Toast(torrents.Count == 1 ? "Category set" : $"Category set on {torrents.Count} torrents", ToastSeverity.Success);
            return true;
        }
        Toast(failures == torrents.Count ? "Couldn't set category" : $"Couldn't set category on {failures} of {torrents.Count}", ToastSeverity.Error);
        return false;
    }

    /// <summary>Replaces a torrent's tag set entirely (not a merge - the Go side's own <c>SetTags</c> works the same way). Used by the "Set Tags..." context menu prompt's code-behind.</summary>
    public async Task<bool> SetTagsAsync(string infoHash, IReadOnlyList<string> tags)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            await _client.PatchTorrentAsync(infoHash, new PatchTorrentOptions(Tags: tags), CancellationToken.None);
            await RefreshAsync();
            Toast("Tags set", ToastSeverity.Success);
            return true;
        }
        catch (Exception ex)
        {
            Toast($"Couldn't set tags: {ex.Message}", ToastSeverity.Error);
            return false;
        }
    }

    /// <summary>
    /// Sets one torrent's own down/up rate limit, in KiB/s (0 = unlimited).
    /// Used by the "Set Speed Limits..." context menu prompt's code-behind.
    /// There's nothing to pre-fill the dialog with - gottrentd's
    /// <c>TorrentSummary</c>/<c>TorrentDetail</c> never report a torrent's
    /// current per-torrent limit back (same write-only shape as
    /// <see cref="EnableSequentialAsync"/>/<see cref="DisableSequentialAsync"/>
    /// - there is no GET route for it either).
    /// </summary>
    public async Task<bool> SetSpeedLimitsAsync(string infoHash, long downLimitKB, long upLimitKB)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            await _client.PatchTorrentAsync(infoHash, new PatchTorrentOptions(DownLimitKB: downLimitKB, UpLimitKB: upLimitKB), CancellationToken.None);
            await RefreshAsync();
            Toast("Speed limits set", ToastSeverity.Success);
            return true;
        }
        catch (Exception ex)
        {
            Toast($"Couldn't set speed limits: {ex.Message}", ToastSeverity.Error);
            return false;
        }
    }

    /// <summary>
    /// Moves a torrent's downloaded content to a new directory
    /// (<c>engine.MoveData</c> on the Go side: stops the torrent, renames
    /// the content root, restarts under the new directory, resumes from
    /// existing resume data with no re-download or re-verify). Used by the
    /// "Set Location..." context menu prompt's code-behind. Unlike the
    /// speed-limit dialog above, this one genuinely can pre-fill - the
    /// current save path is already sitting in
    /// <see cref="DetailTorrent"/>.<c>DownloadDir</c> for whatever torrent
    /// is selected.
    /// </summary>
    public async Task<bool> SetLocationAsync(string infoHash, string downloadDir)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            await _client.PatchTorrentAsync(infoHash, new PatchTorrentOptions(DownloadDir: downloadDir), CancellationToken.None);
            await RefreshAsync();
            Toast("Location updated", ToastSeverity.Success);
            return true;
        }
        catch (Exception ex)
        {
            Toast($"Couldn't move torrent: {ex.Message}", ToastSeverity.Error);
            return false;
        }
    }

    /// <summary>Adds a tracker to the selected torrent. Used by the Trackers tab's "Add" button code-behind.</summary>
    public async Task<bool> AddTrackerAsync(string url)
    {
        if (_client is null || SelectedTorrent is null)
        {
            return false;
        }
        try
        {
            await _client.AddTrackerAsync(SelectedTorrent.InfoHash, url, CancellationToken.None);
            await LoadSelectedDetailAsync();
            Toast("Tracker added", ToastSeverity.Success);
            return true;
        }
        catch (Exception ex)
        {
            Toast($"Couldn't add tracker: {ex.Message}", ToastSeverity.Error);
            return false;
        }
    }

    /// <summary>Changes one file's priority on the selected torrent. Used by the Files tab's priority selector.</summary>
    public async Task<bool> SetFilePriorityAsync(int fileIndex, string priority)
    {
        if (_client is null || SelectedTorrent is null)
        {
            return false;
        }
        try
        {
            await _client.SetFilePriorityAsync(SelectedTorrent.InfoHash, fileIndex, priority, CancellationToken.None);
            await LoadSelectedDetailAsync();
            return true;
        }
        catch (Exception ex)
        {
            Toast($"Couldn't set file priority: {ex.Message}", ToastSeverity.Error);
            return false;
        }
    }

    /// <summary>
    /// The shared implementation behind every simple one-shot torrent
    /// action (Pause, Verify, move-queue, …): <paramref name="actionLabel"/>
    /// (e.g. "pause") only ever appears in the failure toast - none of
    /// these get a success toast, since the grid's own state/column
    /// update (directly, or via optimistic UI for Pause/Resume) already
    /// is the success feedback, and a toast on every single click of a
    /// frequent action would just be noise.
    /// </summary>
    /// <summary>
    /// The shared implementation behind every simple torrent action
    /// (Pause, Verify, move-queue, …) - Stage 4's multi-select made this
    /// genuinely per-torrent rather than closing over a single
    /// <c>SelectedTorrent</c>, since several of its callers (move-queue,
    /// force-start toggle) need each torrent's own current state, not
    /// one shared value, even before multi-select existed. Runs
    /// sequentially, not fanned out with <c>Task.WhenAll</c> - a local
    /// daemon on the same machine has no real need for that concurrency
    /// machinery, the same reasoning this project's plain (non-resilience-
    /// wrapped) <c>HttpClient</c> choice already rests on. Only one
    /// failure toast for the whole batch, not one per torrent - a
    /// selection-wide action failing for 2 of 12 torrents shouldn't
    /// paper the screen in toasts.
    /// </summary>
    private async Task RunTorrentActionAsync(Func<IEngineClient, TorrentRowViewModel, Task> action, string actionLabel)
    {
        if (_client is null || SelectedTorrents.Count == 0)
        {
            return;
        }
        var client = _client;
        var torrents = SelectedTorrents.ToList();
        var failures = 0;
        Exception? lastFailure = null;
        foreach (var torrent in torrents)
        {
            try
            {
                await action(client, torrent);
            }
            catch (Exception ex)
            {
                failures++;
                lastFailure = ex;
            }
        }
        // Only refresh when at least one action actually succeeded -
        // matching the pre-multi-select behavior of never refreshing
        // after a single torrent's action failed. A refresh after a
        // total failure risks folding the same underlying problem into
        // ConnectionError too (a real RefreshAsync failure sets it),
        // which would misattribute a per-action failure as a lost
        // connection instead of leaving it to this method's own toast.
        if (failures < torrents.Count)
        {
            await RefreshAsync();
        }
        if (failures > 0)
        {
            var message = torrents.Count == 1
                ? $"Couldn't {actionLabel}: {lastFailure!.Message}"
                : $"Couldn't {actionLabel} {failures} of {torrents.Count}: {lastFailure!.Message}";
            Toast(message, ToastSeverity.Error);
        }
    }

    /// <summary>
    /// gottrentd's add route has no "start sequential" option of its own
    /// (<c>engine.AddOptions</c> only ever carries Category/Tags) - the Add
    /// Torrent dialog's sequential checkbox is applied as a follow-up PATCH
    /// instead, the same existing route <see cref="EnableSequentialAsync"/>
    /// uses. Best-effort: a failure here doesn't fail or undo the add
    /// itself, since the torrent already exists regardless - it just stays
    /// on rarest-first, exactly as if the checkbox had never been ticked.
    /// </summary>
    private async Task ApplySequentialIfRequestedAsync(string infoHash, bool sequential)
    {
        if (!sequential || _client is null)
        {
            return;
        }
        try
        {
            await _client.PatchTorrentAsync(infoHash, new PatchTorrentOptions(Sequential: true), CancellationToken.None);
        }
        catch (Exception ex)
        {
            Toast($"Added, but couldn't enable sequential download: {ex.Message}", ToastSeverity.Error);
        }
    }

    /// <summary>
    /// Adds a torrent from a magnet link. Used directly by
    /// <c>MainViewModelTests</c> and by the add-torrent dialog's
    /// code-behind, which has no ViewModel of its own - a file picker
    /// is inherently UI chrome with nothing worth unit-testing.
    /// </summary>
    public async Task<bool> AddMagnetAsync(string magnet, string? category, string? downloadDir, IReadOnlyList<string>? tags = null, bool sequential = false)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            var infoHash = await _client.AddMagnetAsync(magnet, category, tags, downloadDir, CancellationToken.None);
            await ApplySequentialIfRequestedAsync(infoHash, sequential);
            RecordRecentDownloadDir(downloadDir);
            AddTorrentError = null;
            await RefreshAsync();
            return true;
        }
        catch (Exception ex)
        {
            AddTorrentError = ex.Message;
            return false;
        }
    }

    /// <summary>Adds a torrent by having gottrentd itself fetch it from an http/https URL - the same code-behind reasoning as <see cref="AddMagnetAsync"/>.</summary>
    public async Task<bool> AddUrlAsync(string url, string? category, string? downloadDir, IReadOnlyList<string>? tags = null, bool sequential = false)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            var infoHash = await _client.AddUrlAsync(url, category, tags, downloadDir, CancellationToken.None);
            await ApplySequentialIfRequestedAsync(infoHash, sequential);
            RecordRecentDownloadDir(downloadDir);
            AddTorrentError = null;
            await RefreshAsync();
            return true;
        }
        catch (Exception ex)
        {
            AddTorrentError = ex.Message;
            return false;
        }
    }

    public async Task<bool> AddTorrentFileAsync(byte[] fileBytes, string fileName, string? category, string? downloadDir, IReadOnlyList<string>? tags = null, bool sequential = false)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            var infoHash = await _client.AddTorrentFileAsync(fileBytes, fileName, category, tags, downloadDir, CancellationToken.None);
            await ApplySequentialIfRequestedAsync(infoHash, sequential);
            RecordRecentDownloadDir(downloadDir);
            AddTorrentError = null;
            await RefreshAsync();
            return true;
        }
        catch (Exception ex)
        {
            AddTorrentError = ex.Message;
            return false;
        }
    }

    /// <summary>
    /// Reads the fleet-wide rate limits currently in effect. Used by the
    /// preferences dialog's code-behind (no ViewModel of its own, same
    /// reasoning as the add-torrent dialog) to populate its fields on open.
    /// </summary>
    public Task<SessionLimits> GetSessionLimitsAsync() =>
        _client is null ? Task.FromResult(new SessionLimits(0, 0)) : _client.GetSessionLimitsAsync(CancellationToken.None);

    public Task<SessionLimits> SetSessionLimitsAsync(long? downLimitKB, long? upLimitKB) =>
        _client is null
            ? throw new InvalidOperationException("Not connected to gottrentd.")
            : _client.SetSessionLimitsAsync(downLimitKB, upLimitKB, CancellationToken.None);

    /// <summary>
    /// Stage 6's disk-space guard for a real .torrent file about to be
    /// added: previews it (never adds - see <see cref="Models.PreviewResponse"/>'s
    /// own doc comment) and compares its size against free space at
    /// <paramref name="downloadDir"/>, returning a warning message if it
    /// won't fit or null if it's fine. Returns null (nothing to warn
    /// about, silently) rather than throwing when there's no client, no
    /// explicit save path (the app has no way to know what gottrentd's
    /// own default/category path would resolve to without asking it to
    /// commit to an Add first, which defeats "before adding"), or the
    /// preview/diskspace calls themselves fail - this check is advisory
    /// only, never a reason to block Add or show an unrelated error.
    /// </summary>
    public Task<string?> CheckDiskSpaceForFileAsync(byte[] fileBytes, string fileName, string? downloadDir) =>
        CheckDiskSpaceAsync(downloadDir, () => _client!.PreviewFileAsync(fileBytes, fileName, CancellationToken.None));

    /// <summary>The URL half of <see cref="CheckDiskSpaceForFileAsync"/>.</summary>
    public Task<string?> CheckDiskSpaceForUrlAsync(string url, string? downloadDir) =>
        CheckDiskSpaceAsync(downloadDir, () => _client!.PreviewUrlAsync(url, CancellationToken.None));

    private async Task<string?> CheckDiskSpaceAsync(string? downloadDir, Func<Task<PreviewResponse>> preview)
    {
        if (_client is null || string.IsNullOrWhiteSpace(downloadDir))
        {
            return null;
        }
        try
        {
            var previewResult = await preview();
            var space = await _client.GetDiskSpaceAsync(downloadDir, CancellationToken.None);
            if (previewResult.TotalLength <= space.FreeBytes)
            {
                return null;
            }
            return $"This torrent needs {FormatBytes(previewResult.TotalLength)} but only {FormatBytes(space.FreeBytes)} is free at \"{downloadDir}\".";
        }
        catch
        {
            return null;
        }
    }

    private static string FormatBytes(long bytes)
    {
        string[] units = ["B", "KiB", "MiB", "GiB", "TiB"];
        double value = bytes;
        var unitIndex = 0;
        while (value >= 1024 && unitIndex < units.Length - 1)
        {
            value /= 1024;
            unitIndex++;
        }
        return FormattableString.Invariant($"{value:0.#} {units[unitIndex]}");
    }

    /// <summary>
    /// Starts the periodic auto-refresh - a real Avalonia UI-thread
    /// timer, so this is only ever called once a real
    /// Application/Dispatcher exists (from App.axaml.cs, never from
    /// tests, which call <see cref="RefreshAsync"/> directly instead).
    ///
    /// <para>
    /// <c>Tick</c>'s handler is guarded against re-entrancy: a slow or
    /// hung daemon used to let a second (and third, and...) tick start
    /// its own <see cref="RefreshAsync"/> while an earlier one was still
    /// awaiting, all interleaving at <c>await</c> points on the UI thread
    /// against the same bound collections. Guarding here (rather than
    /// inside <see cref="RefreshAsync"/> itself) keeps every other direct
    /// caller of <see cref="RefreshAsync"/> - an action that just
    /// succeeded, tests - unaffected: they always get a real, immediate
    /// refresh, only the timer's own re-entry is skipped.
    /// </para>
    /// </summary>
    public void StartAutoRefresh()
    {
        if (_timer is not null)
        {
            return;
        }
        _timer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(2) };
        _timer.Tick += async (_, _) =>
        {
            if (_autoRefreshInFlight)
            {
                return;
            }
            _autoRefreshInFlight = true;
            try
            {
                await RefreshAsync();
            }
            finally
            {
                _autoRefreshInFlight = false;
            }
        };
        _timer.Start();
        _ = RefreshAsync();
    }

    /// <summary>
    /// Starts the 1 Hz per-peer contribution poll - same
    /// only-called-once-from-App.axaml.cs convention as
    /// <see cref="StartAutoRefresh"/>/<see cref="StartLiveEvents"/>, same
    /// re-entrancy guard reasoning too.
    /// Separate timer from the main 2s auto-refresh: ROADMAP.md's 6.2
    /// specifically calls for peer rows at 1 Hz, and ticking the whole
    /// torrent list/detail pane that often would be far more REST calls
    /// than the peer rate computation actually needs.
    /// </summary>
    public void StartPeerRefresh()
    {
        if (_peerTimer is not null)
        {
            return;
        }
        _peerTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(1) };
        _peerTimer.Tick += async (_, _) =>
        {
            if (_peerRefreshInFlight)
            {
                return;
            }
            _peerRefreshInFlight = true;
            try
            {
                await RefreshPeerRatesAsync();
            }
            finally
            {
                _peerRefreshInFlight = false;
            }
        };
        _peerTimer.Start();
    }

    /// <summary>
    /// Stops the background loops (auto-refresh, peer refresh, the live
    /// event socket loop) and disposes the current engine client - called
    /// once from <c>App.axaml.cs</c>'s real-exit path (the tray's "Exit,"
    /// never the hide-to-tray close), so nothing keeps polling or holding
    /// a real socket open after the process has decided to actually quit.
    /// </summary>
    public void Dispose()
    {
        _timer?.Stop();
        _peerTimer?.Stop();
        _lifetimeCts.Cancel();
        _detailLoadCts?.Cancel();
        (_client as IDisposable)?.Dispose();
        _lifetimeCts.Dispose();
    }
}
