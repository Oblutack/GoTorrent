using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Tests;

public sealed class MainViewModelTests
{
    private static TorrentSummary MakeTorrent(string name) => new(
        InfoHash: "0102030405060708090a0b0c0d0e0f1011121314",
        Name: name,
        State: "Downloading",
        Downloaded: 100,
        Uploaded: 0,
        Left: 900,
        TotalLength: 1000,
        NumPieces: 10,
        HavePieces: 1,
        PeerCount: 2,
        SeedRatio: 0,
        Private: false,
        Category: null,
        Tags: null,
        QueuePosition: 0,
        ForceStart: false);

    private static TorrentDetail MakeDetail(string name) => new(
        InfoHash: "0102030405060708090a0b0c0d0e0f1011121314",
        Name: name,
        State: "Downloading",
        Downloaded: 100,
        Uploaded: 0,
        Left: 900,
        TotalLength: 1000,
        NumPieces: 10,
        HavePieces: 1,
        PeerCount: 2,
        SeedRatio: 0,
        Private: false,
        Category: null,
        Tags: null,
        QueuePosition: 0,
        ForceStart: false,
        Source: "magnet",
        DownloadDir: "/downloads",
        ContentPath: "/downloads/" + name,
        InEndgame: false,
        SeedingDurationSeconds: 0);

    private static (MainViewModel ViewModel, FakeEngineClient Client, FakeSettingsStore Settings) MakeViewModel()
    {
        var client = new FakeEngineClient();
        var settings = new FakeSettingsStore();
        var viewModel = new MainViewModel(_ => client, settings);
        return (viewModel, client, settings);
    }

    [Fact]
    public void Connect_WithAValidAddress_Succeeds()
    {
        var (viewModel, _, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";

        viewModel.ConnectCommand.Execute(null);

        Assert.True(viewModel.IsConnected);
        Assert.Null(viewModel.ConnectionError);
    }

    [Fact]
    public void Connect_WithAnInvalidAddress_SetsAnError()
    {
        var (viewModel, _, _) = MakeViewModel();
        viewModel.BaseAddressInput = "not a url";
        viewModel.TokenInput = "a-token";

        viewModel.ConnectCommand.Execute(null);

        Assert.False(viewModel.IsConnected);
        Assert.NotNull(viewModel.ConnectionError);
    }

    [Fact]
    public void Connect_PersistsSettingsForNextLaunch()
    {
        var (viewModel, _, settings) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";

        viewModel.ConnectCommand.Execute(null);

        var saved = settings.Load();
        Assert.Equal("http://127.0.0.1:6880/", saved.BaseAddress);
        Assert.Equal("a-token", saved.Token);
    }

    [Fact]
    public async Task RefreshAsync_BeforeConnecting_DoesNothing()
    {
        var (viewModel, _, _) = MakeViewModel();

        await viewModel.RefreshAsync();

        Assert.Empty(viewModel.Torrents);
        Assert.Null(viewModel.Session);
    }

    [Fact]
    public async Task RefreshAsync_AfterConnecting_PopulatesTorrentsAndSession()
    {
        var (viewModel, client, _) = MakeViewModel();
        client.Torrents.Add(MakeTorrent("ubuntu.iso"));
        client.Session = new SessionStats(1, 1, 0, 0, 0, 100, 0, 2);
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        await viewModel.RefreshAsync();

        var torrent = Assert.Single(viewModel.Torrents);
        Assert.Equal("ubuntu.iso", torrent.Name);
        Assert.Equal(1, viewModel.Session!.TorrentCount);
    }

    [Fact]
    public async Task RefreshAsync_WhenTheClientFails_SetsAnErrorWithoutThrowing()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Failure = new HttpRequestException("connection refused");

        await viewModel.RefreshAsync();

        Assert.Equal("connection refused", viewModel.ConnectionError);
    }

    [Fact]
    public async Task AddMagnetAsync_AddsAndRefreshes()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("added.iso"));

        var ok = await viewModel.AddMagnetAsync("magnet:?xt=urn:btih:abc", category: null, downloadDir: null);

        Assert.True(ok);
        Assert.Equal("magnet:?xt=urn:btih:abc", client.LastAddedMagnet);
        Assert.Single(viewModel.Torrents);
    }

    [Fact]
    public async Task AddMagnetAsync_WhenTheClientFails_SetsAnErrorAndReturnsFalse()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Failure = new EngineRequestException("already added");

        var ok = await viewModel.AddMagnetAsync("magnet:?xt=urn:btih:abc", category: null, downloadDir: null);

        Assert.False(ok);
        Assert.Equal("already added", viewModel.AddTorrentError);
    }

    [Fact]
    public async Task RefreshAsync_PreservesTheSelectionAcrossARefresh()
    {
        // Regression guard: RefreshAsync used to replace Torrents with a
        // brand new ObservableCollection every tick, which reset the
        // DataGrid's SelectedItem to null - a context-menu action started
        // right before the next 2s auto-refresh tick silently did nothing.
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        client.Torrents[0] = MakeTorrent("ubuntu.iso");
        await viewModel.RefreshAsync();

        Assert.NotNull(viewModel.SelectedTorrent);
        Assert.Equal(torrent.InfoHash, viewModel.SelectedTorrent!.InfoHash);
    }

    [Fact]
    public async Task PauseSelectedCommand_PausesTheSelectedTorrentAndRefreshes()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.PauseSelectedCommand.ExecuteAsync(null);

        Assert.Equal([torrent.InfoHash], client.PausedHashes);
    }

    [Fact]
    public async Task ResumeSelectedCommand_ResumesTheSelectedTorrent()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.ResumeSelectedCommand.ExecuteAsync(null);

        Assert.Equal([torrent.InfoHash], client.ResumedHashes);
    }

    [Fact]
    public async Task DeleteSelectedWithDataCommand_DeletesWithDeleteDataTrue()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.DeleteSelectedWithDataCommand.ExecuteAsync(null);

        Assert.Equal([(torrent.InfoHash, true)], client.DeletedHashes);
    }

    [Fact]
    public async Task PauseSelectedCommand_WithNoSelectionDoesNothing()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        await viewModel.PauseSelectedCommand.ExecuteAsync(null);

        Assert.Empty(client.PausedHashes);
    }

    [Fact]
    public async Task LoadSelectedDetailAsync_WithNoSelectionClearsTheDetailPane()
    {
        var (viewModel, _, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        await viewModel.LoadSelectedDetailAsync();

        Assert.Null(viewModel.DetailTorrent);
        Assert.Empty(viewModel.DetailFiles);
        Assert.Empty(viewModel.DetailPeers);
        Assert.Empty(viewModel.DetailTrackers);
    }

    [Fact]
    public async Task LoadSelectedDetailAsync_WithASelectionPopulatesAllFourTabs()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];
        client.Detail = MakeDetail("ubuntu.iso");
        client.Files.Add(new FileEntry(["ubuntu.iso"], 1000, "normal"));
        client.Peers.Add(new PeerEntry("127.0.0.1:6881", true, 100, 0, false, true, false, true, 0.1));
        client.Trackers.Add(new TrackerEntry("udp://tracker.example/announce", DateTimeOffset.UtcNow, null, 5, 1));

        await viewModel.LoadSelectedDetailAsync();

        Assert.Equal(torrent.InfoHash, client.DetailRequestedHashes.Last());
        Assert.Equal("ubuntu.iso", viewModel.DetailTorrent!.Name);
        Assert.Single(viewModel.DetailFiles);
        Assert.Single(viewModel.DetailPeers);
        Assert.Single(viewModel.DetailTrackers);
    }

    [Fact]
    public async Task RefreshAsync_ReloadsTheDetailPaneForTheSelectedTorrent()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        client.Detail = MakeDetail("ubuntu.iso");
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.RefreshAsync();

        Assert.NotNull(viewModel.DetailTorrent);
        Assert.Equal("ubuntu.iso", viewModel.DetailTorrent!.Name);
    }

    [Fact]
    public async Task GetSessionLimitsAsync_ReadsTheCurrentLimits()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.SessionLimits = new SessionLimits(500, 100);

        var limits = await viewModel.GetSessionLimitsAsync();

        Assert.Equal(500, limits.DownLimitKB);
        Assert.Equal(100, limits.UpLimitKB);
    }

    [Fact]
    public async Task SetSessionLimitsAsync_UpdatesTheLimits()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        var limits = await viewModel.SetSessionLimitsAsync(downLimitKB: 200, upLimitKB: null);

        Assert.Equal(200, limits.DownLimitKB);
        Assert.Equal(200, client.SessionLimits.DownLimitKB);
    }

    [Fact]
    public void Constructor_WithSavedSettings_ConnectsAutomatically()
    {
        var settings = new FakeSettingsStore();
        settings.Save(new DesktopSettings("http://127.0.0.1:6880/", "saved-token"));
        var client = new FakeEngineClient();

        var viewModel = new MainViewModel(_ => client, settings);

        Assert.True(viewModel.IsConnected);
        Assert.Equal("http://127.0.0.1:6880/", viewModel.BaseAddressInput);
    }
}
