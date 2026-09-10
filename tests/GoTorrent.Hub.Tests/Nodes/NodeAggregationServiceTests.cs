using GoTorrent.Hub.Core.Engine;
using GoTorrent.Hub.Core.Nodes;
using Microsoft.Extensions.Logging.Abstractions;

namespace GoTorrent.Hub.Tests.Nodes;

public sealed class NodeAggregationServiceTests
{
    private static EngineNode MakeNode(string name, bool enabled = true) => new()
    {
        Name = name,
        BaseAddress = new Uri($"http://{name}.example/"),
        Token = "irrelevant-in-tests",
        Enabled = enabled,
    };

    private static TorrentSummary MakeTorrent(string name) => new(
        InfoHash: "0102030405060708090a0b0c0d0e0f1011121314",
        Name: name,
        State: "Seeding",
        Downloaded: 0,
        Uploaded: 0,
        Left: 0,
        TotalLength: 0,
        NumPieces: 0,
        HavePieces: 0,
        PeerCount: 0,
        SeedRatio: 0,
        Private: false,
        Category: null,
        Tags: null,
        QueuePosition: 0,
        ForceStart: false);

    [Fact]
    public async Task GetAggregatedTorrentsAsync_TagsEachTorrentWithItsOwnNode()
    {
        var repo = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var nodeA = MakeNode("node-a");
        var nodeB = MakeNode("node-b");
        repo.Seed(nodeA);
        repo.Seed(nodeB);
        factory.SetClient(nodeA, new FakeEngineClient { Torrents = [MakeTorrent("a.iso")] });
        factory.SetClient(nodeB, new FakeEngineClient { Torrents = [MakeTorrent("b.iso")] });
        var service = new NodeAggregationService(repo, factory, NullLogger<NodeAggregationService>.Instance);

        var result = await service.GetAggregatedTorrentsAsync(CancellationToken.None);

        Assert.Equal(2, result.Count);
        Assert.Contains(result, r => r.NodeId == nodeA.Id && r.Torrent.Name == "a.iso");
        Assert.Contains(result, r => r.NodeId == nodeB.Id && r.Torrent.Name == "b.iso");
    }

    [Fact]
    public async Task GetAggregatedTorrentsAsync_SkipsDisabledNodes()
    {
        var repo = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var disabled = MakeNode("disabled-node", enabled: false);
        repo.Seed(disabled);
        // Deliberately no client configured for it - if the service tried
        // to contact a disabled node this test would throw instead of
        // just asserting an empty result, which is a stronger check than
        // merely asserting emptiness would be on its own.

        var service = new NodeAggregationService(repo, factory, NullLogger<NodeAggregationService>.Instance);
        var result = await service.GetAggregatedTorrentsAsync(CancellationToken.None);

        Assert.Empty(result);
    }

    [Fact]
    public async Task GetAggregatedTorrentsAsync_ContinuesPastAnUnreachableNode()
    {
        var repo = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var broken = MakeNode("broken-node");
        var healthy = MakeNode("healthy-node");
        repo.Seed(broken);
        repo.Seed(healthy);
        factory.SetClient(broken, new FakeEngineClient { Failure = new HttpRequestException("connection refused") });
        factory.SetClient(healthy, new FakeEngineClient { Torrents = [MakeTorrent("healthy.iso")] });
        var service = new NodeAggregationService(repo, factory, NullLogger<NodeAggregationService>.Instance);

        var result = await service.GetAggregatedTorrentsAsync(CancellationToken.None);

        var summary = Assert.Single(result);
        Assert.Equal(healthy.Id, summary.NodeId);
    }

    [Fact]
    public async Task GetNodeStatusesAsync_ReportsDisabledOnlineAndOfflineCorrectly()
    {
        var repo = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var disabled = MakeNode("disabled-node", enabled: false);
        var online = MakeNode("online-node");
        var offline = MakeNode("offline-node");
        repo.Seed(disabled);
        repo.Seed(online);
        repo.Seed(offline);
        var session = new SessionStats(1, 1, 0, 0, 0, 100, 200, 1);
        factory.SetClient(online, new FakeEngineClient { Session = session });
        factory.SetClient(offline, new FakeEngineClient { Failure = new HttpRequestException("timed out") });
        var service = new NodeAggregationService(repo, factory, NullLogger<NodeAggregationService>.Instance);

        var statuses = await service.GetNodeStatusesAsync(CancellationToken.None);

        var disabledStatus = statuses.Single(s => s.NodeId == disabled.Id);
        Assert.False(disabledStatus.Enabled);
        Assert.False(disabledStatus.Reachable);

        var onlineStatus = statuses.Single(s => s.NodeId == online.Id);
        Assert.True(onlineStatus.Reachable);
        Assert.Equal(session, onlineStatus.Session);

        var offlineStatus = statuses.Single(s => s.NodeId == offline.Id);
        Assert.True(offlineStatus.Enabled);
        Assert.False(offlineStatus.Reachable);
        Assert.NotNull(offlineStatus.Error);
    }
}
