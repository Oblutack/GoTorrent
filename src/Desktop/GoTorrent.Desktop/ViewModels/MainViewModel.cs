using System.Collections.ObjectModel;
using System.IO;
using System.Linq;
using Avalonia.Threading;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.ViewModels;

public partial class MainViewModel : ViewModelBase
{
    /// <summary>How many speed-graph samples to keep - one sessionStats WS message arrives per second, so this is a rolling five-minute window.</summary>
    private const int MaxSpeedSamples = 300;

    private readonly Func<EngineOptions, IEngineClient> _clientFactory;
    private readonly ISettingsStore _settingsStore;
    private readonly IEventStream _eventStream;
    private readonly TimeProvider _timeProvider;
    private readonly IAutostartService _autostartService;
    private readonly IFileAssociationService _fileAssociationService;
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

    [ObservableProperty]
    public partial ObservableCollection<TorrentSummary> Torrents { get; set; } = [];

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
    public partial TorrentSummary? SelectedTorrent { get; set; }

    [ObservableProperty]
    public partial string? AddTorrentError { get; set; }

    [ObservableProperty]
    public partial TorrentDetail? DetailTorrent { get; set; }

    [ObservableProperty]
    public partial ObservableCollection<FileEntry> DetailFiles { get; set; } = [];

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

    public MainViewModel() : this(options => new EngineClient(options), new FileSettingsStore(), new WebSocketEventStream(), TimeProvider.System, new WindowsAutostartService(), new WindowsFileAssociationService())
    {
    }

    /// <summary>
    /// The <paramref name="clientFactory"/>/<paramref name="settingsStore"/>
    /// seams are what make this testable without a real gottrentd or
    /// real file I/O - tests pass a fake client factory and an
    /// in-memory settings store.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore)
        : this(clientFactory, settingsStore, new WebSocketEventStream(), TimeProvider.System, new WindowsAutostartService(), new WindowsFileAssociationService())
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
        : this(clientFactory, settingsStore, eventStream, timeProvider, new WindowsAutostartService(), new WindowsFileAssociationService())
    {
    }

    /// <summary>
    /// <paramref name="autostartService"/> is the same kind of seam again -
    /// tests use a fake so "is GoTorrent registered to launch at login"
    /// never depends on (or mutates) the real Windows registry.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService)
        : this(clientFactory, settingsStore, eventStream, timeProvider, autostartService, new WindowsFileAssociationService())
    {
    }

    /// <summary>
    /// <paramref name="fileAssociationService"/> - same seam again, for
    /// the same reason as <paramref name="autostartService"/>.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore, IEventStream eventStream, TimeProvider timeProvider, IAutostartService autostartService, IFileAssociationService fileAssociationService)
    {
        _clientFactory = clientFactory;
        _settingsStore = settingsStore;
        _eventStream = eventStream;
        _timeProvider = timeProvider;
        _autostartService = autostartService;
        _fileAssociationService = fileAssociationService;

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

    [RelayCommand]
    private void Connect() => TryConnect(BaseAddressInput, TokenInput, persist: true);

    private void TryConnect(string baseAddress, string token, bool persist)
    {
        try
        {
            var options = new EngineOptions(new Uri(baseAddress), token);
            _client = _clientFactory(options);
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
            var selectedHash = SelectedTorrent?.InfoHash;
            Torrents = new ObservableCollection<TorrentSummary>(torrents);
            // Replacing the collection on every 2s auto-refresh tick resets
            // the DataGrid's SelectedItem to null - re-locate the same
            // torrent by hash so a context-menu action started right
            // before a refresh still has something to act on.
            SelectedTorrent = selectedHash is null ? null : Torrents.FirstOrDefault(t => t.InfoHash == selectedHash);
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
    /// Fetches the detail pane's four tabs for whatever torrent is
    /// currently selected. Called after every auto-refresh tick (so the
    /// detail pane stays live while a torrent is selected) and directly
    /// from the View when the user picks a different row, for an instant
    /// update instead of waiting out the rest of the 2s interval.
    /// Clears the detail pane rather than erroring when nothing is
    /// selected - that's a normal state, not a failure.
    /// </summary>
    public async Task LoadSelectedDetailAsync()
    {
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
            DetailTorrent = await _client.GetTorrentDetailAsync(hash, CancellationToken.None);
            DetailFiles = new ObservableCollection<FileEntry>(await _client.GetFilesAsync(hash, CancellationToken.None));
            DetailTrackers = new ObservableCollection<TrackerEntry>(await _client.GetTrackersAsync(hash, CancellationToken.None));
            var pieces = await _client.GetPiecesAsync(hash, CancellationToken.None);
            PieceHave = new ObservableCollection<bool>(pieces.ToHaveArray());
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
        }
        await RefreshPeerRatesAsync();
    }

    /// <summary>
    /// Polls the selected torrent's peers and turns each one's cumulative
    /// Downloaded/Uploaded into a KiB/s "contribution" rate - gottrentd's
    /// <c>GET .../peers</c> reports running totals per peer, not a rate,
    /// and there is no WS message that streams per-peer stats the way
    /// <c>sessionStats</c> streams fleet-wide ones, so this has to poll
    /// and diff itself. Runs at 1 Hz via <see cref="StartPeerRefresh"/>
    /// and once immediately whenever the selection changes (from
    /// <see cref="LoadSelectedDetailAsync"/>), matching the fleet-wide
    /// speed graph's own "first sample has no rate yet" shape.
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
        while (true)
        {
            if (_connectedOptions is { } options)
            {
                try
                {
                    await foreach (var ev in _eventStream.ConnectAsync(options, CancellationToken.None))
                    {
                        var captured = ev;
                        await Dispatcher.UIThread.InvokeAsync(() => HandleEvent(captured));
                    }
                }
                catch
                {
                    // Connection dropped or gottrentd unreachable - retry below.
                }
            }
            await Task.Delay(TimeSpan.FromSeconds(2));
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
        }
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
    /// </summary>
    public void StartAutoRefresh()
    {
        if (_timer is not null)
        {
            return;
        }
        _timer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(2) };
        _timer.Tick += async (_, _) => await RefreshAsync();
        _timer.Start();
        _ = RefreshAsync();
    }

    /// <summary>
    /// Starts the 1 Hz per-peer contribution poll - same
    /// only-called-once-from-App.axaml.cs convention as
    /// <see cref="StartAutoRefresh"/>/<see cref="StartLiveEvents"/>.
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
        _peerTimer.Tick += async (_, _) => await RefreshPeerRatesAsync();
        _peerTimer.Start();
    }
}
