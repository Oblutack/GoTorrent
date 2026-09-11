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
