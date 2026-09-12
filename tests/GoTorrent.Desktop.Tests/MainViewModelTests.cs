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

    private static (MainViewModel ViewModel, FakeEngineClient Client, FixedTimeProvider Clock) MakeViewModelWithClock()
    {
        var client = new FakeEngineClient();
        var settings = new FakeSettingsStore();
        var clock = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var viewModel = new MainViewModel(_ => client, settings, new FakeEventStream(), clock);
        return (viewModel, client, clock);
    }

    private static (MainViewModel ViewModel, FakeAutostartService Autostart) MakeViewModelWithAutostart(bool initiallyEnabled = false)
    {
        var autostart = new FakeAutostartService { Enabled = initiallyEnabled };
        var viewModel = new MainViewModel(_ => new FakeEngineClient(), new FakeSettingsStore(), new FakeEventStream(), TimeProvider.System, autostart);
        return (viewModel, autostart);
    }

    private static (MainViewModel ViewModel, FakeEngineClient Client, FakeFileAssociationService FileAssociation) MakeViewModelWithFileAssociation(bool initiallyRegistered = false)
    {
        var client = new FakeEngineClient();
        var fileAssociation = new FakeFileAssociationService { Registered = initiallyRegistered };
        var viewModel = new MainViewModel(_ => client, new FakeSettingsStore(), new FakeEventStream(), TimeProvider.System, new FakeAutostartService(), fileAssociation);
        return (viewModel, client, fileAssociation);
    }

    private static (MainViewModel ViewModel, FakeEngineClient Client, FakeDaemonLauncher Daemon) MakeViewModelWithDaemonLauncher()
    {
        var client = new FakeEngineClient();
        var daemon = new FakeDaemonLauncher();
        var viewModel = new MainViewModel(_ => client, new FakeSettingsStore(), new FakeEventStream(), TimeProvider.System, new FakeAutostartService(), new FakeFileAssociationService(), daemon);
        return (viewModel, client, daemon);
    }

    private static (MainViewModel ViewModel, FakeEngineClient Client, FakeDesktopNotifier Notifier) MakeViewModelWithDesktopNotifier()
    {
        var client = new FakeEngineClient();
        var notifier = new FakeDesktopNotifier();
        var viewModel = new MainViewModel(_ => client, new FakeSettingsStore(), new FakeEventStream(), TimeProvider.System, new FakeAutostartService(), new FakeFileAssociationService(), new FakeDaemonLauncher(), notifier);
        return (viewModel, client, notifier);
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
    public async Task RefreshAsync_KeepsTheSameRowInstanceAcrossRefreshesEvenWhenUnchanged()
    {
        // The actual root cause RefreshAsync_PreservesTheSelectionAcrossARefresh
        // guards the symptom of: TorrentSummary is a record (value equality),
        // so CommunityToolkit's generated setter used to silently skip
        // reassigning SelectedTorrent whenever a freshly-fetched record was
        // "equal" to the old one - leaving it pointing at an instance no
        // longer present in the rebuilt DisplayedTorrents. A stable
        // TorrentRowViewModel per torrent (updated in place, never
        // replaced) means the *same object reference* survives every
        // refresh, changed or not - this pins that down directly rather
        // than only checking InfoHash equality, which a bug like the
        // original one could still satisfy by accident.
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("ubuntu.iso"));
        await viewModel.RefreshAsync();
        var firstRow = viewModel.Torrents[0];
        viewModel.SelectedTorrent = firstRow;

        // Genuinely unchanged data on the next poll, same as an idle
        // seeding torrent between two 2s ticks.
        await viewModel.RefreshAsync();

        Assert.Same(firstRow, viewModel.Torrents[0]);
        Assert.Same(firstRow, viewModel.SelectedTorrent);
        Assert.Contains(firstRow, viewModel.DisplayedTorrents);
    }

    [Fact]
    public async Task RefreshAsync_UpdatesAnExistingRowInPlaceRatherThanReplacingIt()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("ubuntu.iso") with { Downloaded = 100 });
        await viewModel.RefreshAsync();
        var row = viewModel.Torrents[0];

        client.Torrents[0] = MakeTorrent("ubuntu.iso") with { Downloaded = 500 };
        await viewModel.RefreshAsync();

        Assert.Same(row, viewModel.Torrents[0]);
        Assert.Equal(500, row.Downloaded);
    }

    [Fact]
    public async Task RefreshAsync_RemovesARowAndClearsSelectionWhenTheTorrentIsGone()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("ubuntu.iso"));
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        client.Torrents.Clear();
        await viewModel.RefreshAsync();

        Assert.Empty(viewModel.Torrents);
        Assert.Empty(viewModel.DisplayedTorrents);
        Assert.Null(viewModel.SelectedTorrent);
    }

    [Fact]
    public async Task ApplyFilter_KeepsTheSameSidebarFilterInstanceForAnUnchangedCategory()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("movie") with { Category = "Movies" });
        await viewModel.RefreshAsync();
        var moviesFilter = viewModel.SidebarFilters.Single(f => f.Label == "Movies");
        viewModel.SelectedFilter = moviesFilter;

        client.Torrents.Add(MakeTorrent("show") with { InfoHash = "9999999999999999999999999999999999999999", Category = "Movies" });
        await viewModel.RefreshAsync();

        Assert.Same(moviesFilter, viewModel.SidebarFilters.Single(f => f.Label == "Movies"));
        Assert.Same(moviesFilter, viewModel.SelectedFilter);
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
    public async Task LoadSelectedDetailAsync_WithASelectionPopulatesGeneralFilesAndTrackers()
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
        client.Trackers.Add(new TrackerEntry("udp://tracker.example/announce", DateTimeOffset.UtcNow, null, 5, 1));

        await viewModel.LoadSelectedDetailAsync();

        Assert.Equal(torrent.InfoHash, client.DetailRequestedHashes.Last());
        Assert.Equal("ubuntu.iso", viewModel.DetailTorrent!.Name);
        Assert.Single(viewModel.DetailFiles);
        Assert.Single(viewModel.DetailTrackers);
    }

    [Fact]
    public async Task OnTorrentSelectionChanged_CallsBothLoadSelectedDetailAndRefreshPeerRates()
    {
        // Peers is deliberately not one of LoadSelectedDetailAsync's own
        // four tabs anymore (see its own doc comment) - the real
        // MainWindow.axaml.cs's OnTorrentSelectionChanged calls both
        // methods together on a manual selection change, which is what
        // this pins down instead of relying on a side effect.
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];
        client.Detail = MakeDetail("ubuntu.iso");
        client.Peers.Add(new PeerEntry("127.0.0.1:6881", true, 100, 0, false, true, false, true, 0.1));

        await viewModel.LoadSelectedDetailAsync();
        await viewModel.RefreshPeerRatesAsync();

        Assert.Single(viewModel.DetailPeers);
    }

    [Fact]
    public async Task LoadSelectedDetailAsync_ASlowSupersededCallNeverOverwritesTheNewerSelection()
    {
        // The real bug: select torrent A (slow to respond), then quickly
        // select torrent B (fast) - A's four awaits used to resolve after
        // B's already had, silently overwriting the detail pane with A's
        // (now wrong) data. A's own GetTorrentDetailAsync call is gated
        // open forever (simulating "slow"), so if the fix didn't actually
        // cancel it, this test would hang instead of failing - a stronger
        // guarantee than a timing-based assertion could give.
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrentA = MakeTorrent("A") with { InfoHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" };
        var torrentB = MakeTorrent("B") with { InfoHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" };
        client.Torrents.Add(torrentA);
        client.Torrents.Add(torrentB);
        await viewModel.RefreshAsync();
        client.DetailsByHash[torrentA.InfoHash] = MakeDetail("A");
        client.DetailsByHash[torrentB.InfoHash] = MakeDetail("B");
        client.DetailGatesByHash[torrentA.InfoHash] = new TaskCompletionSource();

        viewModel.SelectedTorrent = viewModel.Torrents.Single(t => t.InfoHash == torrentA.InfoHash);
        var slowLoad = viewModel.LoadSelectedDetailAsync();

        viewModel.SelectedTorrent = viewModel.Torrents.Single(t => t.InfoHash == torrentB.InfoHash);
        await viewModel.LoadSelectedDetailAsync();
        await slowLoad;

        Assert.Equal("B", viewModel.DetailTorrent!.Name);
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
    public async Task LoadSelectedDetailAsync_PopulatesThePieceMapFromTheBitfield()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];
        client.Detail = MakeDetail(torrent.Name);
        // 10 pieces, bitfield 0b10110000 -> pieces 0, 2, 3 have.
        client.Pieces = new PiecesInfo(NumPieces: 10, HaveCount: 3, Bitfield: [0b1011_0000, 0b0000_0000]);

        await viewModel.LoadSelectedDetailAsync();

        Assert.Equal(10, viewModel.PieceHave.Count);
        Assert.True(viewModel.PieceHave[0]);
        Assert.False(viewModel.PieceHave[1]);
        Assert.True(viewModel.PieceHave[2]);
        Assert.True(viewModel.PieceHave[3]);
        Assert.False(viewModel.PieceHave[4]);
    }

    [Fact]
    public async Task HandleEvent_PieceVerifiedForTheSelectedTorrent_MarksThatPieceHave()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];
        client.Detail = MakeDetail(torrent.Name);
        client.Pieces = new PiecesInfo(NumPieces: 4, HaveCount: 0, Bitfield: [0b0000_0000]);
        await viewModel.LoadSelectedDetailAsync();

        viewModel.HandleEvent(new WsEvent("pieceVerified", DateTimeOffset.UtcNow, torrent.InfoHash, null, null, PieceIndex: 2, Session: null));

        Assert.True(viewModel.PieceHave[2]);
        Assert.False(viewModel.PieceHave[0]);
    }

    [Fact]
    public async Task HandleEvent_PieceVerifiedForADifferentTorrent_IsIgnored()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];
        client.Detail = MakeDetail(torrent.Name);
        client.Pieces = new PiecesInfo(NumPieces: 4, HaveCount: 0, Bitfield: [0b0000_0000]);
        await viewModel.LoadSelectedDetailAsync();

        viewModel.HandleEvent(new WsEvent("pieceVerified", DateTimeOffset.UtcNow, "deadbeef00000000000000000000000000000000", null, null, PieceIndex: 2, Session: null));

        Assert.False(viewModel.PieceHave[2]);
    }

    [Fact]
    public async Task HandleEvent_TorrentStateChangedToSeeding_ShowsACompletionNotification()
    {
        var (viewModel, client, notifier) = MakeViewModelWithDesktopNotifier();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();

        viewModel.HandleEvent(new WsEvent("torrentStateChanged", DateTimeOffset.UtcNow, torrent.InfoHash, "Seeding", null, null, null));

        var notification = Assert.Single(notifier.Notifications);
        Assert.Equal("ubuntu.iso", notification.Message);
    }

    [Fact]
    public async Task HandleEvent_TorrentStateChangedToSeedingTwice_OnlyNotifiesOnce()
    {
        var (viewModel, client, notifier) = MakeViewModelWithDesktopNotifier();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("ubuntu.iso");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();

        var ev = new WsEvent("torrentStateChanged", DateTimeOffset.UtcNow, torrent.InfoHash, "Seeding", null, null, null);
        viewModel.HandleEvent(ev);
        viewModel.HandleEvent(ev);

        Assert.Single(notifier.Notifications);
    }

    [Fact]
    public void HandleEvent_TorrentStateChangedToADifferentState_DoesNotNotify()
    {
        var (viewModel, _, notifier) = MakeViewModelWithDesktopNotifier();

        viewModel.HandleEvent(new WsEvent("torrentStateChanged", DateTimeOffset.UtcNow, "0102030405060708090a0b0c0d0e0f1011121314", "Downloading", null, null, null));

        Assert.Empty(notifier.Notifications);
    }

    [Fact]
    public void HandleEvent_TwoSessionStatsMessagesOneSecondApart_RecordsAKnownRate()
    {
        var (viewModel, _, clock) = MakeViewModelWithClock();

        viewModel.HandleEvent(new WsEvent("sessionStats", clock.Now, null, null, null, null, new SessionStats(0, 0, 0, 0, 0, TotalDownloaded: 1024, TotalUploaded: 512, 0)));
        clock.Now = clock.Now.AddSeconds(1);
        viewModel.HandleEvent(new WsEvent("sessionStats", clock.Now, null, null, null, null, new SessionStats(0, 0, 0, 0, 0, TotalDownloaded: 3072, TotalUploaded: 512, 0)));

        // (3072 - 1024) bytes over 1s = 2048 B/s = 2 KiB/s.
        Assert.Equal(2, viewModel.LatestDownloadRateKBps);
        Assert.Equal(0, viewModel.LatestUploadRateKBps);
        Assert.Equal(2, Assert.Single(viewModel.DownloadRateHistory));
    }

    [Fact]
    public void HandleEvent_FirstSessionStatsMessage_RecordsNoSampleYet()
    {
        var (viewModel, _, clock) = MakeViewModelWithClock();

        viewModel.HandleEvent(new WsEvent("sessionStats", clock.Now, null, null, null, null, new SessionStats(0, 0, 0, 0, 0, TotalDownloaded: 1024, TotalUploaded: 0, 0)));

        Assert.Empty(viewModel.DownloadRateHistory);
    }

    [Fact]
    public async Task RefreshPeerRatesAsync_FirstPoll_RecordsZeroRateForEachPeer()
    {
        var (viewModel, client, _) = MakeViewModelWithClock();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        viewModel.SelectedTorrent = new TorrentRowViewModel(MakeTorrent("ubuntu.iso"));
        client.Peers.Add(new PeerEntry("127.0.0.1:6881", true, Downloaded: 4096, Uploaded: 0, false, true, false, true, 0.1));

        await viewModel.RefreshPeerRatesAsync();

        var row = Assert.Single(viewModel.DetailPeers);
        Assert.Equal("127.0.0.1:6881", row.Addr);
        Assert.Equal(0, row.DownloadRateKBps);
    }

    [Fact]
    public async Task RefreshPeerRatesAsync_SecondPollOneSecondLater_RecordsAKnownRate()
    {
        var (viewModel, client, clock) = MakeViewModelWithClock();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        viewModel.SelectedTorrent = new TorrentRowViewModel(MakeTorrent("ubuntu.iso"));
        client.Peers.Add(new PeerEntry("127.0.0.1:6881", true, Downloaded: 1024, Uploaded: 512, false, true, false, true, 0.1));
        await viewModel.RefreshPeerRatesAsync();

        clock.Now = clock.Now.AddSeconds(1);
        client.Peers[0] = client.Peers[0] with { Downloaded = 3072 };
        await viewModel.RefreshPeerRatesAsync();

        // (3072 - 1024) bytes over 1s = 2048 B/s = 2 KiB/s.
        var row = Assert.Single(viewModel.DetailPeers);
        Assert.Equal(2, row.DownloadRateKBps);
        Assert.Equal(0, row.UploadRateKBps);
    }

    [Fact]
    public async Task RefreshPeerRatesAsync_SwitchingSelectedTorrent_StartsAFreshSeries()
    {
        var (viewModel, client, clock) = MakeViewModelWithClock();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        viewModel.SelectedTorrent = new TorrentRowViewModel(MakeTorrent("ubuntu.iso"));
        client.Peers.Add(new PeerEntry("127.0.0.1:6881", true, Downloaded: 1024, Uploaded: 0, false, true, false, true, 0.1));
        await viewModel.RefreshPeerRatesAsync();

        clock.Now = clock.Now.AddSeconds(1);
        // A different torrent, coincidentally sharing a peer address, with
        // a much larger total - naively diffing against the previous
        // torrent's totals would produce a nonsense huge rate.
        viewModel.SelectedTorrent = new TorrentRowViewModel(MakeTorrent("debian.iso") with { InfoHash = "aabbccddeeff00112233445566778899aabbccd" });
        client.Peers[0] = client.Peers[0] with { Downloaded = 500_000 };
        await viewModel.RefreshPeerRatesAsync();

        var row = Assert.Single(viewModel.DetailPeers);
        Assert.Equal(0, row.DownloadRateKBps);
    }

    [Fact]
    public void Constructor_ReadsAutostartStateFromTheAutostartService()
    {
        var (viewModel, _) = MakeViewModelWithAutostart(initiallyEnabled: true);

        Assert.True(viewModel.AutostartEnabled);
    }

    [Fact]
    public void SetAutostart_EnablesItThroughTheAutostartService()
    {
        var (viewModel, autostart) = MakeViewModelWithAutostart(initiallyEnabled: false);

        viewModel.SetAutostart(true);

        Assert.True(autostart.Enabled);
        Assert.True(viewModel.AutostartEnabled);
    }

    [Fact]
    public void SetAutostart_DisablesItThroughTheAutostartService()
    {
        var (viewModel, autostart) = MakeViewModelWithAutostart(initiallyEnabled: true);

        viewModel.SetAutostart(false);

        Assert.False(autostart.Enabled);
        Assert.False(viewModel.AutostartEnabled);
    }

    [Fact]
    public void Constructor_ReadsFileAssociationStateFromTheService()
    {
        var (viewModel, _, _) = MakeViewModelWithFileAssociation(initiallyRegistered: true);

        Assert.True(viewModel.FileAssociationEnabled);
    }

    [Fact]
    public void SetFileAssociation_EnablesItThroughTheService()
    {
        var (viewModel, _, fileAssociation) = MakeViewModelWithFileAssociation(initiallyRegistered: false);

        viewModel.SetFileAssociation(true);

        Assert.True(fileAssociation.Registered);
        Assert.True(viewModel.FileAssociationEnabled);
    }

    [Fact]
    public void SetFileAssociation_DisablesItThroughTheService()
    {
        var (viewModel, _, fileAssociation) = MakeViewModelWithFileAssociation(initiallyRegistered: true);

        viewModel.SetFileAssociation(false);

        Assert.False(fileAssociation.Registered);
        Assert.False(viewModel.FileAssociationEnabled);
    }

    [Fact]
    public async Task AddFromArgumentAsync_WithAMagnetLink_AddsItAsAMagnet()
    {
        var (viewModel, client, _) = MakeViewModelWithFileAssociation();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        await viewModel.AddFromArgumentAsync("magnet:?xt=urn:btih:abc");

        Assert.Equal("magnet:?xt=urn:btih:abc", client.LastAddedMagnet);
    }

    [Fact]
    public async Task AddFromArgumentAsync_WithATorrentFilePath_AddsItAsAFile()
    {
        var (viewModel, client, _) = MakeViewModelWithFileAssociation();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var path = Path.Combine(Path.GetTempPath(), $"gotorrent-test-{Guid.NewGuid():N}.torrent");
        await File.WriteAllBytesAsync(path, [1, 2, 3]);
        try
        {
            await viewModel.AddFromArgumentAsync(path);

            Assert.Equal(Path.GetFileName(path), client.LastAddedFileName);
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public async Task AddFromArgumentAsync_WithNeitherAMagnetNorAnExistingFile_SetsAnError()
    {
        var (viewModel, _, _) = MakeViewModelWithFileAssociation();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        await viewModel.AddFromArgumentAsync(@"C:\does\not\exist.torrent");

        Assert.NotNull(viewModel.AddTorrentError);
    }

    [Fact]
    public async Task AddFromArgumentAsync_WhenNotConnected_SetsAnError()
    {
        var (viewModel, _, _) = MakeViewModelWithFileAssociation();

        await viewModel.AddFromArgumentAsync("magnet:?xt=urn:btih:abc");

        Assert.NotNull(viewModel.AddTorrentError);
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

    [Fact]
    public void Constructor_WithSavedSettings_LoadsStartMinimized()
    {
        var settings = new FakeSettingsStore();
        settings.Save(new DesktopSettings("http://127.0.0.1:6880/", "saved-token", StartMinimized: true));

        var viewModel = new MainViewModel(_ => new FakeEngineClient(), settings);

        Assert.True(viewModel.StartMinimized);
    }

    [Fact]
    public void SetStartMinimized_PersistsWithoutTouchingSavedConnectionSettings()
    {
        var (viewModel, _, settings) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        viewModel.SetStartMinimized(true);

        var saved = settings.Load();
        Assert.True(saved.StartMinimized);
        Assert.Equal("http://127.0.0.1:6880/", saved.BaseAddress);
        Assert.Equal("a-token", saved.Token);
    }

    [Fact]
    public void Connect_DoesNotResetAPreviouslySavedStartMinimized()
    {
        var (viewModel, _, settings) = MakeViewModel();
        settings.Save(new DesktopSettings(null, null, StartMinimized: true));
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";

        viewModel.ConnectCommand.Execute(null);

        Assert.True(settings.Load().StartMinimized);
    }

    [Fact]
    public void Constructor_ReadsDaemonAvailabilityFromTheLauncher()
    {
        var (viewModel, _, daemon) = MakeViewModelWithDaemonLauncher();
        daemon.IsAvailable = true;

        Assert.True(viewModel.DaemonAvailable);
    }

    [Fact]
    public async Task StartLocalDaemonCommand_WithNoExistingToken_SpawnsAndConnectsUsingTheReturnedToken()
    {
        var (viewModel, _, daemon) = MakeViewModelWithDaemonLauncher();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        daemon.ExistingToken = null;
        daemon.TokenToReturnOnStart = "spawned-token";

        await viewModel.StartLocalDaemonCommand.ExecuteAsync(null);

        Assert.Equal(1, daemon.StartCallCount);
        Assert.Equal("127.0.0.1:6880", daemon.LastStartApiAddress);
        Assert.True(viewModel.IsConnected);
        Assert.True(daemon.IsRunning);
        Assert.Null(viewModel.ConnectionError);
    }

    [Fact]
    public async Task StartLocalDaemonCommand_WithAReachableExistingToken_AttachesWithoutSpawning()
    {
        var (viewModel, client, daemon) = MakeViewModelWithDaemonLauncher();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        daemon.ExistingToken = "already-running-token";
        client.Failure = null;

        await viewModel.StartLocalDaemonCommand.ExecuteAsync(null);

        Assert.Equal(0, daemon.StartCallCount);
        Assert.True(viewModel.IsConnected);
    }

    [Fact]
    public async Task StartLocalDaemonCommand_WithAnUnreachableExistingToken_FallsBackToSpawning()
    {
        var (viewModel, client, daemon) = MakeViewModelWithDaemonLauncher();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        daemon.ExistingToken = "stale-token";
        daemon.TokenToReturnOnStart = "spawned-token";
        client.Failure = new InvalidOperationException("connection refused");

        await viewModel.StartLocalDaemonCommand.ExecuteAsync(null);

        Assert.Equal(1, daemon.StartCallCount);
        Assert.True(viewModel.IsConnected);
    }

    [Fact]
    public async Task StartLocalDaemonCommand_WhenSpawnFails_SetsAConnectionErrorAndStaysDisconnected()
    {
        var (viewModel, _, daemon) = MakeViewModelWithDaemonLauncher();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        daemon.ExistingToken = null;
        daemon.TokenToReturnOnStart = null;

        await viewModel.StartLocalDaemonCommand.ExecuteAsync(null);

        Assert.False(viewModel.IsConnected);
        Assert.NotNull(viewModel.ConnectionError);
    }

    [Fact]
    public void WeOwnRunningDaemon_ReflectsTheLaunchersIsRunning()
    {
        var (viewModel, _, daemon) = MakeViewModelWithDaemonLauncher();

        Assert.False(viewModel.WeOwnRunningDaemon);

        daemon.TokenToReturnOnStart = "a-token";
        _ = daemon.StartAsync("127.0.0.1:6880", CancellationToken.None);

        Assert.True(viewModel.WeOwnRunningDaemon);
    }

    [Fact]
    public void StopLocalDaemon_CallsThroughToTheLauncher()
    {
        var (viewModel, _, daemon) = MakeViewModelWithDaemonLauncher();

        viewModel.StopLocalDaemon();

        Assert.Equal(1, daemon.StopCallCount);
    }

    [Fact]
    public async Task ApplyFilter_WithNoFilterOrSearch_ShowsEveryTorrent()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("a") with { InfoHash = "1111111111111111111111111111111111111111", State = "Seeding" });
        client.Torrents.Add(MakeTorrent("b") with { InfoHash = "2222222222222222222222222222222222222222", State = "Paused" });

        await viewModel.RefreshAsync();

        Assert.Equal(2, viewModel.DisplayedTorrents.Count);
    }

    [Fact]
    public async Task ApplyFilter_BySeedingStatus_OnlyShowsSeedingTorrents()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("seeder") with { InfoHash = "1111111111111111111111111111111111111111", State = "Seeding" });
        client.Torrents.Add(MakeTorrent("leecher") with { InfoHash = "2222222222222222222222222222222222222222", State = "Downloading" });
        await viewModel.RefreshAsync();

        viewModel.SelectedFilter = new SidebarFilter(SidebarFilter.SeedingKey, "Seeding");

        Assert.Equal(["seeder"], viewModel.DisplayedTorrents.Select(t => t.Name));
    }

    [Fact]
    public async Task ApplyFilter_BySearchText_MatchesNameCaseInsensitively()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("Ubuntu.iso") with { InfoHash = "1111111111111111111111111111111111111111" });
        client.Torrents.Add(MakeTorrent("Debian.iso") with { InfoHash = "2222222222222222222222222222222222222222" });
        await viewModel.RefreshAsync();

        viewModel.SearchText = "ubuntu";

        Assert.Equal(["Ubuntu.iso"], viewModel.DisplayedTorrents.Select(t => t.Name));
    }

    [Fact]
    public async Task ApplyFilter_IncludesADistinctCategoryFromTorrents()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("movie") with { InfoHash = "1111111111111111111111111111111111111111", Category = "Movies" });

        await viewModel.RefreshAsync();

        Assert.Contains(viewModel.SidebarFilters, f => f.Label == "Movies");
    }

    [Fact]
    public async Task ToggleForceStartCommand_FlipsForceStart()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("a") with { ForceStart = false };
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.ToggleForceStartCommand.ExecuteAsync(null);

        Assert.True(client.Torrents[0].ForceStart);
    }

    [Fact]
    public async Task MoveQueueTopCommand_SetsQueuePositionToZero()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("a") with { QueuePosition = 3 });
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.MoveQueueTopCommand.ExecuteAsync(null);

        Assert.Equal(0, client.Torrents[0].QueuePosition);
    }

    [Fact]
    public async Task MoveQueueUpCommand_DecrementsQueuePosition()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("a") with { QueuePosition = 3 });
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.MoveQueueUpCommand.ExecuteAsync(null);

        Assert.Equal(2, client.Torrents[0].QueuePosition);
    }

    [Fact]
    public async Task MoveQueueDownCommand_IncrementsQueuePosition()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        client.Torrents.Add(MakeTorrent("a") with { QueuePosition = 3 });
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        await viewModel.MoveQueueDownCommand.ExecuteAsync(null);

        Assert.Equal(4, client.Torrents[0].QueuePosition);
    }

    [Fact]
    public async Task SetCategoryAsync_SetsTheCategory()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("a");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();

        var ok = await viewModel.SetCategoryAsync(torrent.InfoHash, "Movies");

        Assert.True(ok);
        Assert.Equal("Movies", client.Torrents[0].Category);
    }

    [Fact]
    public async Task AddTrackerAsync_CallsThroughWithTheSelectedTorrentsHash()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("a");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        var ok = await viewModel.AddTrackerAsync("http://example.com/announce");

        Assert.True(ok);
        Assert.Equal([(torrent.InfoHash, "http://example.com/announce")], client.AddedTrackers);
    }

    [Fact]
    public async Task AddTrackerAsync_WithNoSelectedTorrent_ReturnsFalse()
    {
        var (viewModel, _, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        var ok = await viewModel.AddTrackerAsync("http://example.com/announce");

        Assert.False(ok);
    }

    [Fact]
    public async Task SetFilePriorityAsync_CallsThroughWithTheSelectedTorrentsHash()
    {
        var (viewModel, client, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);
        var torrent = MakeTorrent("a");
        client.Torrents.Add(torrent);
        await viewModel.RefreshAsync();
        viewModel.SelectedTorrent = viewModel.Torrents[0];

        var ok = await viewModel.SetFilePriorityAsync(2, "high");

        Assert.True(ok);
        Assert.Equal([(torrent.InfoHash, 2, "high")], client.SetFilePriorities);
    }

    [Fact]
    public async Task SetFilePriorityAsync_WithNoSelectedTorrent_ReturnsFalse()
    {
        var (viewModel, _, _) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        var ok = await viewModel.SetFilePriorityAsync(0, "high");

        Assert.False(ok);
    }
}
