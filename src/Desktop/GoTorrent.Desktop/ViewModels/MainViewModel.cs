using System.Collections.ObjectModel;
using System.IO;
using System.Linq;
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
    private readonly HashSet<string> _notifiedCompletionHashes = [];
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

    [ObservableProperty]
    public partial SessionStats? Session { get; set; }

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

    public MainViewModel() : this(options => new EngineClient(options), new FileSettingsStore(), new WebSocketEventStream(), TimeProvider.System, new WindowsAutostartService(), new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier())
    {
    }

    /// <summary>
    /// The <paramref name="clientFactory"/>/<paramref name="settingsStore"/>
    /// seams are what make this testable without a real gottrentd or
    /// real file I/O - tests pass a fake client factory and an
    /// in-memory settings store.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore)
        : this(clientFactory, settingsStore, new WebSocketEventStream(), TimeProvider.System, new WindowsAutostartService(), new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier())
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
        : this(clientFactory, settingsStore, eventStream, timeProvider, new WindowsAutostartService(), new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier())
    {
    }

    /// <summary>
    /// <paramref name="autostartService"/> is the same kind of seam again -
    /// tests use a fake so "is GoTorrent registered to launch at login"
    /// never depends on (or mutates) the real Windows registry.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, new WindowsFileAssociationService(), new DaemonLauncher(), new WindowsDesktopNotifier())
    {
    }

    /// <summary>
    /// <paramref name="fileAssociationService"/> - same seam again, for
    /// the same reason as <paramref name="autostartService"/>.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, fileAssociationService, new DaemonLauncher(), new WindowsDesktopNotifier())
    {
    }

    /// <summary>
    /// <paramref name="daemonLauncher"/> - same seam again: tests use a
    /// fake so "spawn gottrentd" never starts a real process.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService, IDaemonLauncher daemonLauncher)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, fileAssociationService, daemonLauncher, new WindowsDesktopNotifier())
    {
    }

    /// <summary>
    /// <paramref name="desktopNotifier"/> - same seam again: tests use a
    /// fake so "notify on completion" never shows a real OS notification.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService, IDaemonLauncher daemonLauncher, IDesktopNotifier desktopNotifier)
    {
        _clientFactory = clientFactory;
        _settingsStore = settingsStore;
        _eventStream = eventStream;
        _timeProvider = timeProvider;
        _autostartService = autostartService;
        _fileAssociationService = fileAssociationService;
        _daemonLauncher = daemonLauncher;
        _desktopNotifier = desktopNotifier;

        var settings = _settingsStore.Load();
        StartMinimized = settings.StartMinimized;
        AutostartEnabled = _autostartService.IsEnabled();
        FileAssociationEnabled = _fileAssociationService.IsRegistered();
        if (settings.IsConfigured)
        {
            BaseAddressInput = settings.BaseAddress!;
            TryConnect(settings.BaseAddress!, settings.Token!, persist: false);
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

    [RelayCommand]
    private void Connect() => TryConnect(BaseAddressInput, TokenInput, persist: true);

    private void TryConnect(string baseAddress, string token, bool persist)
    {
        try
        {
            var options = new EngineOptions(new Uri(baseAddress), token);
            var newClient = _clientFactory(options);
            // Reconnecting (a second Connect click, or daemon supervision
            // attaching after a spawn) used to just overwrite _client,
            // leaking the previous one's real HttpClient/socket handles.
            (_client as IDisposable)?.Dispose();
            _client = newClient;
            _connectedOptions = options;
            IsConnected = true;
            ConnectionError = null;
            if (persist)
            {
                // `with` rather than a fresh DesktopSettings - this must not
                // clobber StartMinimized (or any other future preference)
                // back to its default every time the user hits Connect.
                _settingsStore.Save(_settingsStore.Load() with { BaseAddress = baseAddress, Token = token });
            }
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
            IsConnected = false;
        }
    }

    /// <summary>
    /// Daemon supervision's "attach if running, spawn if not": first tries
    /// <see cref="BaseAddressInput"/> with whatever token
    /// <see cref="IDaemonLauncher.TryReadExistingToken"/> finds (a real API
    /// call, not just constructing a client - see <see cref="TryReachAsync"/>,
    /// since <see cref="TryConnect"/> itself never makes one), and only
    /// spawns a fresh gottrentd if that fails or no token file exists yet.
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
            if (existingToken is not null && await TryReachAsync(baseUri, existingToken))
            {
                TryConnect(BaseAddressInput, existingToken, persist: true);
                return;
            }

            var token = await _daemonLauncher.StartAsync(baseUri.Authority, CancellationToken.None);
            if (token is null)
            {
                ConnectionError = "Could not start gottrentd - it may already be running on a different address, or the executable could not be found next to this app.";
                return;
            }
            TryConnect(BaseAddressInput, token, persist: true);
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
                row = new TorrentRowViewModel(summary);
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
        var categories = Torrents
            .Select(t => t.Category)
            .Where(c => !string.IsNullOrWhiteSpace(c))
            .Distinct()
            .OrderBy(c => c, StringComparer.OrdinalIgnoreCase)
            .Select(c => SidebarFilter.Category(c!));

        var filters = new List<SidebarFilter> { AllFilter, DownloadingFilter, SeedingFilter, PausedFilter, ErrorFilter };
        filters.AddRange(categories);
        SyncCollection(SidebarFilters, filters);

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
        var filtered = Torrents.Where(t => selectedFilter.Matches(t.State, t.Category));
        if (search.Length > 0)
        {
            filtered = filtered.Where(t => t.Name.Contains(search, StringComparison.OrdinalIgnoreCase));
        }
        SyncCollection(DisplayedTorrents, filtered.ToList());
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
            return;
        }
        var hash = SelectedTorrent.InfoHash;
        try
        {
            var detail = await _client.GetTorrentDetailAsync(hash, token);
            var files = await _client.GetFilesAsync(hash, token);
            var trackers = await _client.GetTrackersAsync(hash, token);
            var pieces = await _client.GetPiecesAsync(hash, token);
            DetailTorrent = detail;
            DetailFiles = new ObservableCollection<FileEntry>(files);
            DetailTrackers = new ObservableCollection<TrackerEntry>(trackers);
            PieceHave = new ObservableCollection<bool>(pieces.ToHaveArray());
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
            rows.Add(new PeerRow(peer.Addr, peer.Outbound, downKBps, upKBps, peer.Progress, peer.AmChoking, peer.PeerChoking));
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

    [RelayCommand]
    private Task PauseSelectedAsync() => RunTorrentActionAsync(client => client.PauseAsync(SelectedTorrent!.InfoHash, CancellationToken.None));

    [RelayCommand]
    private Task ResumeSelectedAsync() => RunTorrentActionAsync(client => client.ResumeAsync(SelectedTorrent!.InfoHash, CancellationToken.None));

    [RelayCommand]
    private Task DeleteSelectedAsync() => RunTorrentActionAsync(client => client.DeleteAsync(SelectedTorrent!.InfoHash, deleteData: false, CancellationToken.None));

    [RelayCommand]
    private Task DeleteSelectedWithDataAsync() => RunTorrentActionAsync(client => client.DeleteAsync(SelectedTorrent!.InfoHash, deleteData: true, CancellationToken.None));

    [RelayCommand]
    private Task ToggleForceStartAsync() => RunTorrentActionAsync(client =>
        client.PatchTorrentAsync(SelectedTorrent!.InfoHash, new PatchTorrentOptions(ForceStart: !SelectedTorrent!.ForceStart), CancellationToken.None));

    [RelayCommand]
    private Task MoveQueueTopAsync() => RunTorrentActionAsync(client =>
        client.PatchTorrentAsync(SelectedTorrent!.InfoHash, new PatchTorrentOptions(QueuePosition: 0), CancellationToken.None));

    [RelayCommand]
    private Task MoveQueueUpAsync() => RunTorrentActionAsync(client =>
        client.PatchTorrentAsync(SelectedTorrent!.InfoHash, new PatchTorrentOptions(QueuePosition: Math.Max(0, SelectedTorrent!.QueuePosition - 1)), CancellationToken.None));

    [RelayCommand]
    private Task MoveQueueDownAsync() => RunTorrentActionAsync(client =>
        client.PatchTorrentAsync(SelectedTorrent!.InfoHash, new PatchTorrentOptions(QueuePosition: SelectedTorrent!.QueuePosition + 1), CancellationToken.None));

    /// <summary>
    /// gottrentd doesn't report a torrent's current picker strategy
    /// anywhere in <see cref="Models.TorrentSummary"/>/<see cref="Models.TorrentDetail"/>
    /// (it's actor-internal state, not part of either DTO), so there's
    /// nothing to toggle against - two explicit commands instead of one
    /// blind toggle.
    /// </summary>
    [RelayCommand]
    private Task EnableSequentialAsync() => RunTorrentActionAsync(client =>
        client.PatchTorrentAsync(SelectedTorrent!.InfoHash, new PatchTorrentOptions(Sequential: true), CancellationToken.None));

    [RelayCommand]
    private Task DisableSequentialAsync() => RunTorrentActionAsync(client =>
        client.PatchTorrentAsync(SelectedTorrent!.InfoHash, new PatchTorrentOptions(Sequential: false), CancellationToken.None));

    /// <summary>Sets a torrent's category. Used by the "Set Category..." context menu prompt's code-behind (no ViewModel of its own, same reasoning as every other small dialog).</summary>
    public async Task<bool> SetCategoryAsync(string infoHash, string category)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            await _client.PatchTorrentAsync(infoHash, new PatchTorrentOptions(Category: category), CancellationToken.None);
            await RefreshAsync();
            return true;
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
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
            return true;
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
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
            ConnectionError = ex.Message;
            return false;
        }
    }

    private async Task RunTorrentActionAsync(Func<IEngineClient, Task> action)
    {
        if (_client is null || SelectedTorrent is null)
        {
            return;
        }
        try
        {
            await action(_client);
            await RefreshAsync();
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
        }
    }

    /// <summary>
    /// Adds a torrent from a magnet link. Used directly by
    /// <c>MainViewModelTests</c> and by the add-torrent dialog's
    /// code-behind, which has no ViewModel of its own - a file picker
    /// is inherently UI chrome with nothing worth unit-testing.
    /// </summary>
    public async Task<bool> AddMagnetAsync(string magnet, string? category, string? downloadDir)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            await _client.AddMagnetAsync(magnet, category, downloadDir, CancellationToken.None);
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

    public async Task<bool> AddTorrentFileAsync(byte[] fileBytes, string fileName, string? category, string? downloadDir)
    {
        if (_client is null)
        {
            return false;
        }
        try
        {
            await _client.AddTorrentFileAsync(fileBytes, fileName, category, downloadDir, CancellationToken.None);
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
