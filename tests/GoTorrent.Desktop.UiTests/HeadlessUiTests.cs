using System.Linq;
using System.Threading.Tasks;
using Avalonia.Controls;
using Avalonia.Headless;
using Avalonia.Headless.XUnit;
using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Tests;
using GoTorrent.Desktop.ViewModels;
using GoTorrent.Desktop.Views;
using Xunit;

[assembly: AvaloniaTestApplication(typeof(GoTorrent.Desktop.App))]

namespace GoTorrent.Desktop.UiTests;

/// <summary>
/// Real headless-Avalonia UI tests - unlike every test in
/// GoTorrent.Desktop.Tests, which drives <see cref="MainViewModel"/>
/// directly and never touches a real <see cref="Window"/>/
/// <see cref="DataGrid"/>/binding, these build a real
/// <see cref="MainWindow"/> against Avalonia's real headless platform
/// (<c>[AvaloniaFact]</c>, which manages setup/teardown and dispatcher
/// affinity per test). Kept in a separate project from
/// GoTorrent.Desktop.Tests deliberately - see this project's own .csproj
/// comment for the two real, independently-confirmed reasons why (an
/// xunit v2/v3 conflict, and a real live Application.Current contaminating
/// unrelated tests that assume there isn't one).
///
/// <para>
/// This exists because three real bugs in this app's history (see
/// CLAUDE.md's "qBittorrent-style redesign" section: a stack overflow from
/// <c>SidebarFilters</c> reassignment, a <c>NullReferenceException</c> from
/// <c>Clear()</c> transiently nulling <c>SelectedFilter</c>, and a silent
/// selection-loss regression in <c>RefreshAsync</c>) were all invisible to
/// every ViewModel-only test, because all three came from
/// <see cref="Avalonia.Controls.SelectingItemsControl"/>'s real two-way-
/// binding behavior - a real <see cref="DataGrid"/>/<c>ListBox</c> in the
/// loop is what actually exercises that, not a fake. The tests below
/// don't reproduce those three bugs literally (their exact root causes
/// were fixed structurally, by giving <see cref="TorrentRowViewModel"/>
/// stable per-hash identity instead of value-equality records) - see the
/// first test's own doc comment for what was actually confirmed, by
/// deliberately breaking things by hand, to still be worth a real-binding-
/// pipeline test today.
/// </para>
/// </summary>
public class HeadlessUiTests
{
    private static (MainViewModel ViewModel, FakeEngineClient Client) MakeViewModel()
    {
        var client = new FakeEngineClient();
        var settings = new FakeSettingsStore();
        var viewModel = new MainViewModel(_ => client, settings);
        return (viewModel, client);
    }

    private static TorrentSummary MakeTorrent(string name, string infoHash) => new(
        InfoHash: infoHash,
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

    /// <summary>
    /// Select a torrent through the real <see cref="DataGrid"/> (not
    /// <c>viewModel.SelectedTorrent = ...</c> directly), then run a real
    /// <see cref="MainViewModel.RefreshAsync"/> poll cycle, and confirm the
    /// real <see cref="DataGrid.SelectedItem"/> binding still shows it.
    ///
    /// <para>
    /// What this actually protects, confirmed empirically rather than
    /// assumed: <c>ReconcileTorrents</c> reuses the *same*
    /// <see cref="TorrentRowViewModel"/> instance per info hash across a
    /// refresh (<c>UpdateFrom</c>, never a replacement) - deliberately
    /// broken once, by hand, to check what this test would actually catch.
    /// Rebuilding <c>Torrents</c> via <c>Clear()</c>+re-<c>Add()</c> of
    /// those *same* instances did NOT fail this test - Avalonia's real
    /// <c>SelectedItem</c> binding re-matches by reference regardless of
    /// which collection object currently holds it, so <c>SyncCollection</c>'s
    /// remaining value for this particular list is avoiding List-diff
    /// churn, not preventing selection loss. Constructing a *fresh*
    /// <see cref="TorrentRowViewModel"/> for an already-known info hash
    /// instead of reusing it via <c>UpdateFrom</c>, however, broke this
    /// test immediately (a real failed assertion, not a false pass) -
    /// object identity across a refresh, not collection-mutation style, is
    /// the actual thing worth a real binding-pipeline test for here.
    /// </para>
    /// </summary>
    [AvaloniaFact]
    public async Task SelectingATorrentThenRefreshing_PreservesTheRealDataGridSelection()
    {
        var (viewModel, client) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        client.Torrents.Add(MakeTorrent("alpha", "1111111111111111111111111111111111111a"));
        client.Torrents.Add(MakeTorrent("beta", "2222222222222222222222222222222222222b"));
        await viewModel.RefreshAsync();

        var window = new MainWindow { DataContext = viewModel };
        window.AttachViewModel(viewModel);
        window.Show();

        var grid = window.FindControl<DataGrid>("TorrentsGrid");
        Assert.NotNull(grid);

        var beta = viewModel.Torrents.Single(t => t.Name == "beta");
        grid!.SelectedItem = beta;

        Assert.NotNull(viewModel.SelectedTorrent);
        Assert.Equal("beta", viewModel.SelectedTorrent!.Name);
        Assert.Same(beta, grid.SelectedItem);

        // A real poll tick, exactly what the 2s auto-refresh timer does
        // while the app is running.
        await viewModel.RefreshAsync();

        Assert.NotNull(viewModel.SelectedTorrent);
        Assert.Equal("beta", viewModel.SelectedTorrent!.Name);
        Assert.Same(viewModel.SelectedTorrent, grid.SelectedItem);

        window.Close();
    }

    /// <summary>
    /// The complementary case: a torrent that disappears from a later poll
    /// (removed server-side) must clear the real DataGrid's selection, not
    /// leave it dangling on a row no longer present anywhere.
    ///
    /// <para>
    /// Honest finding, also confirmed by deliberately breaking things by
    /// hand: <c>ReconcileTorrents</c>'s own explicit
    /// <c>SelectedTorrent = null</c> guard turned out NOT to be what this
    /// test actually depends on - disabling it entirely still left this
    /// test passing, because <c>SyncCollection</c> genuinely
    /// <c>RemoveAt</c>s the disappeared row (a real <c>CollectionChanged</c>
    /// removal), and Avalonia's own <see cref="DataGrid"/>/
    /// <see cref="Avalonia.Controls.SelectingItemsControl"/> already clears
    /// <c>SelectedItem</c> - and two-way-binds that back to
    /// <c>SelectedTorrent</c> - when the selected item is removed from its
    /// source collection. The ViewModel's own explicit guard is genuinely
    /// redundant here; its real purpose is correctness for a caller with
    /// no live <see cref="DataGrid"/> attached at all (already covered by
    /// a plain ViewModel-only test in <c>MainViewModelTests</c>), not
    /// something this real-binding test is needed to prove. Kept anyway,
    /// as real confirmation the two layers (the explicit guard, and
    /// Avalonia's own automatic behavior) don't fight each other or leave
    /// a gap between them.
    /// </para>
    /// </summary>
    [AvaloniaFact]
    public async Task SelectingATorrentThenItDisappearing_ClearsTheRealDataGridSelection()
    {
        var (viewModel, client) = MakeViewModel();
        viewModel.BaseAddressInput = "http://127.0.0.1:6880/";
        viewModel.TokenInput = "a-token";
        viewModel.ConnectCommand.Execute(null);

        client.Torrents.Add(MakeTorrent("gone-soon", "3333333333333333333333333333333333333c"));
        await viewModel.RefreshAsync();

        var window = new MainWindow { DataContext = viewModel };
        window.AttachViewModel(viewModel);
        window.Show();

        var grid = window.FindControl<DataGrid>("TorrentsGrid");
        Assert.NotNull(grid);
        grid!.SelectedItem = viewModel.Torrents.Single();
        Assert.NotNull(viewModel.SelectedTorrent);

        client.Torrents.Clear();
        await viewModel.RefreshAsync();

        Assert.Null(viewModel.SelectedTorrent);
        Assert.Null(grid.SelectedItem);

        window.Close();
    }
}
