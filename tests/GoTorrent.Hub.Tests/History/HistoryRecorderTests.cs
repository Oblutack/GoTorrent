using GoTorrent.Hub.Core.Engine;
using GoTorrent.Hub.Core.History;
using GoTorrent.Hub.Core.Nodes;
using GoTorrent.Hub.Tests.Nodes;
using Microsoft.Extensions.Logging.Abstractions;

namespace GoTorrent.Hub.Tests.History;

public sealed class HistoryRecorderTests
{
    private static EngineNode MakeNode(string name, bool enabled = true) => new()
    {
        Name = name,
        BaseAddress = new Uri($"http://{name}.example/"),
        Token = "irrelevant-in-tests",
        Enabled = enabled,
    };

    private static TorrentSummary MakeTorrent(string name, long left) => new(
        InfoHash: "0102030405060708090a0b0c0d0e0f1011121314",
        Name: name,
        State: left == 0 ? "Seeding" : "Downloading",
        Downloaded: 1000,
        Uploaded: 500,
        Left: left,
        TotalLength: 1000,
        NumPieces: 1,
        HavePieces: left == 0 ? 1 : 0,
        PeerCount: 0,
        SeedRatio: 0.5,
        Private: false,
        Category: "movies",
        Tags: null,
        QueuePosition: 0,
        ForceStart: false);

    private static (HistoryRecorder Recorder, FakeTorrentHistoryRepository History, FakeSessionSnapshotRepository Snapshots, FixedTimeProvider Time)
        MakeRecorder(FakeEngineNodeRepository nodes, FakeEngineClientFactory factory, HistoryOptions? options = null)
    {
        var aggregation = new NodeAggregationService(nodes, factory, NullLogger<NodeAggregationService>.Instance);
        var history = new FakeTorrentHistoryRepository();
        var snapshots = new FakeSessionSnapshotRepository();
        var time = new FixedTimeProvider(new DateTimeOffset(2026, 9, 10, 12, 0, 0, TimeSpan.Zero));
        var recorder = new HistoryRecorder(aggregation, history, snapshots, options ?? new HistoryOptions(), time, NullLogger<HistoryRecorder>.Instance);
        return (recorder, history, snapshots, time);
    }

    [Fact]
    public async Task RecordAsync_RecordsASnapshotForEachReachableNode()
    {
        var nodes = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var node = MakeNode("node-a");
        nodes.Seed(node);
        var session = new SessionStats(1, 1, 0, 0, 0, 1000, 500, 3);
        factory.SetClient(node, new FakeEngineClient { Session = session, Torrents = [] });
        var (recorder, _, snapshots, time) = MakeRecorder(nodes, factory);

        await recorder.RecordAsync(CancellationToken.None);

        var snapshot = Assert.Single(snapshots.Snapshots);
        Assert.Equal(node.Id, snapshot.NodeId);
        Assert.Equal(session.TotalDownloaded, snapshot.TotalDownloaded);
        Assert.Equal(session.TotalUploaded, snapshot.TotalUploaded);
        Assert.Equal(time.Now, snapshot.CapturedAt);
    }

    [Fact]
    public async Task RecordAsync_SkipsSnapshotForUnreachableOrDisabledNodes()
    {
        var nodes = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var offline = MakeNode("offline-node");
        var disabled = MakeNode("disabled-node", enabled: false);
        nodes.Seed(offline);
        nodes.Seed(disabled);
        factory.SetClient(offline, new FakeEngineClient { Failure = new HttpRequestException("refused") });
        var (recorder, _, snapshots, _) = MakeRecorder(nodes, factory);

        await recorder.RecordAsync(CancellationToken.None);

        Assert.Empty(snapshots.Snapshots);
    }

    [Fact]
    public async Task RecordAsync_ArchivesACompletedTorrentOnceNotTwice()
    {
        var nodes = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var node = MakeNode("node-a");
        nodes.Seed(node);
        factory.SetClient(node, new FakeEngineClient
        {
            Session = new SessionStats(1, 0, 1, 0, 0, 1000, 500, 0),
            Torrents = [MakeTorrent("done.iso", left: 0)],
        });
        var (recorder, history, _, time) = MakeRecorder(nodes, factory);

        await recorder.RecordAsync(CancellationToken.None);
        await recorder.RecordAsync(CancellationToken.None);

        var entry = Assert.Single(history.Entries);
        Assert.Equal(node.Id, entry.NodeId);
        Assert.Equal("done.iso", entry.Name);
        Assert.Equal("movies", entry.Category);
        Assert.Equal(time.Now, entry.CompletedAt);
    }

    [Fact]
    public async Task RecordAsync_DoesNotArchiveAnIncompleteTorrent()
    {
        var nodes = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var node = MakeNode("node-a");
        nodes.Seed(node);
        factory.SetClient(node, new FakeEngineClient
        {
            Session = new SessionStats(1, 1, 0, 0, 0, 100, 0, 1),
            Torrents = [MakeTorrent("in-progress.iso", left: 500)],
        });
        var (recorder, history, _, _) = MakeRecorder(nodes, factory);

        await recorder.RecordAsync(CancellationToken.None);

        Assert.Empty(history.Entries);
    }

    [Fact]
    public async Task RecordAsync_PrunesSnapshotsOlderThanRetention()
    {
        var nodes = new FakeEngineNodeRepository();
        var factory = new FakeEngineClientFactory();
        var node = MakeNode("node-a");
        nodes.Seed(node);
        factory.SetClient(node, new FakeEngineClient { Session = new SessionStats(0, 0, 0, 0, 0, 0, 0, 0) });
        var options = new HistoryOptions { SnapshotRetention = TimeSpan.FromDays(1) };
        var (recorder, _, snapshots, time) = MakeRecorder(nodes, factory, options);

        // A stale snapshot from well before the retention window.
        await snapshots.AddAsync(new SessionSnapshot { NodeId = node.Id, NodeName = node.Name, CapturedAt = time.Now.AddDays(-5) }, CancellationToken.None);

        await recorder.RecordAsync(CancellationToken.None);

        Assert.DoesNotContain(snapshots.Snapshots, s => s.CapturedAt == time.Now.AddDays(-5));
        // The fresh snapshot this same pass just recorded must survive its own prune step.
        Assert.Contains(snapshots.Snapshots, s => s.CapturedAt == time.Now);
    }
}
