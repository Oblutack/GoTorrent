using System.Collections.ObjectModel;
using System.Linq;
using Avalonia.Threading;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.ViewModels;

public partial class MainViewModel : ViewModelBase
{
    private readonly Func<EngineOptions, IEngineClient> _clientFactory;
    private readonly ISettingsStore _settingsStore;
    private IEngineClient? _client;
    private DispatcherTimer? _timer;

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
    public partial ObservableCollection<PeerEntry> DetailPeers { get; set; } = [];

    [ObservableProperty]
    public partial ObservableCollection<TrackerEntry> DetailTrackers { get; set; } = [];

    public MainViewModel() : this(options => new EngineClient(options), new FileSettingsStore())
    {
    }

    /// <summary>
    /// The <paramref name="clientFactory"/>/<paramref name="settingsStore"/>
    /// seams are what make this testable without a real gottrentd or
    /// real file I/O - tests pass a fake client factory and an
    /// in-memory settings store.
    /// </summary>
    public MainViewModel(Func<EngineOptions, IEngineClient> clientFactory, ISettingsStore settingsStore)
    {
        _clientFactory = clientFactory;
        _settingsStore = settingsStore;

        var settings = _settingsStore.Load();
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
            _client = _clientFactory(new EngineOptions(new Uri(baseAddress), token));
            IsConnected = true;
            ConnectionError = null;
            if (persist)
            {
                _settingsStore.Save(new DesktopSettings(baseAddress, token));
            }
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
            IsConnected = false;
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
            return;
        }
        var hash = SelectedTorrent.InfoHash;
        try
        {
            DetailTorrent = await _client.GetTorrentDetailAsync(hash, CancellationToken.None);
            DetailFiles = new ObservableCollection<FileEntry>(await _client.GetFilesAsync(hash, CancellationToken.None));
            DetailPeers = new ObservableCollection<PeerEntry>(await _client.GetPeersAsync(hash, CancellationToken.None));
            DetailTrackers = new ObservableCollection<TrackerEntry>(await _client.GetTrackersAsync(hash, CancellationToken.None));
        }
        catch (Exception ex)
        {
            ConnectionError = ex.Message;
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
}
