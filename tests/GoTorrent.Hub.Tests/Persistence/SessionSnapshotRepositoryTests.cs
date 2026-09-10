using GoTorrent.Hub.Core.History;
using GoTorrent.Hub.Infrastructure.History;

namespace GoTorrent.Hub.Tests.Persistence;

public sealed class SessionSnapshotRepositoryTests : IDisposable
{
    private readonly SqliteDbContextFixture _fixture = new();

    public void Dispose() => _fixture.Dispose();

    private static SessionSnapshot MakeSnapshot(Guid nodeId, DateTimeOffset capturedAt) => new()
    {
        NodeId = nodeId,
        NodeName = "node-a",
        CapturedAt = capturedAt,
        TorrentCount = 1,
        TotalDownloaded = 100,
        TotalUploaded = 50,
    };

    [Fact]
    public async Task AddAsync_ThenGetSinceAsync_RoundTrips()
    {
        using var db = _fixture.CreateContext();
        var repo = new SessionSnapshotRepository(db);
        var nodeId = Guid.NewGuid();
        var now = DateTimeOffset.UtcNow;

        await repo.AddAsync(MakeSnapshot(nodeId, now), CancellationToken.None);

        var found = await repo.GetSinceAsync(now.AddMinutes(-1), null, CancellationToken.None);
        var snapshot = Assert.Single(found);
        Assert.Equal(nodeId, snapshot.NodeId);
        Assert.Equal(100, snapshot.TotalDownloaded);
    }

    [Fact]
    public async Task GetSinceAsync_ExcludesSnapshotsBeforeTheCutoff()
    {
        using var db = _fixture.CreateContext();
        var repo = new SessionSnapshotRepository(db);
        var nodeId = Guid.NewGuid();
        var now = DateTimeOffset.UtcNow;
        await repo.AddAsync(MakeSnapshot(nodeId, now.AddDays(-2)), CancellationToken.None);
        await repo.AddAsync(MakeSnapshot(nodeId, now), CancellationToken.None);

        var found = await repo.GetSinceAsync(now.AddDays(-1), null, CancellationToken.None);

        var snapshot = Assert.Single(found);
        Assert.Equal(now, snapshot.CapturedAt);
    }

    [Fact]
    public async Task GetSinceAsync_FiltersByNodeIdWhenGiven()
    {
        using var db = _fixture.CreateContext();
        var repo = new SessionSnapshotRepository(db);
        var nodeA = Guid.NewGuid();
        var nodeB = Guid.NewGuid();
        var now = DateTimeOffset.UtcNow;
        await repo.AddAsync(MakeSnapshot(nodeA, now), CancellationToken.None);
        await repo.AddAsync(MakeSnapshot(nodeB, now), CancellationToken.None);

        var found = await repo.GetSinceAsync(now.AddMinutes(-1), nodeA, CancellationToken.None);

        var snapshot = Assert.Single(found);
        Assert.Equal(nodeA, snapshot.NodeId);
    }

    [Fact]
    public async Task GetSinceAsync_ReturnsOldestFirst()
    {
        using var db = _fixture.CreateContext();
        var repo = new SessionSnapshotRepository(db);
        var nodeId = Guid.NewGuid();
        var now = DateTimeOffset.UtcNow;
        await repo.AddAsync(MakeSnapshot(nodeId, now), CancellationToken.None);
        await repo.AddAsync(MakeSnapshot(nodeId, now.AddMinutes(-10)), CancellationToken.None);

        var found = await repo.GetSinceAsync(now.AddDays(-1), nodeId, CancellationToken.None);

        Assert.Equal([now.AddMinutes(-10), now], found.Select(s => s.CapturedAt));
    }

    [Fact]
    public async Task PruneOlderThanAsync_RemovesOnlyStaleSnapshots()
    {
        using var db = _fixture.CreateContext();
        var repo = new SessionSnapshotRepository(db);
        var nodeId = Guid.NewGuid();
        var now = DateTimeOffset.UtcNow;
        await repo.AddAsync(MakeSnapshot(nodeId, now.AddDays(-40)), CancellationToken.None);
        await repo.AddAsync(MakeSnapshot(nodeId, now), CancellationToken.None);

        var removed = await repo.PruneOlderThanAsync(now.AddDays(-30), CancellationToken.None);

        Assert.Equal(1, removed);
        var remaining = await repo.GetSinceAsync(DateTimeOffset.MinValue, null, CancellationToken.None);
        var snapshot = Assert.Single(remaining);
        Assert.Equal(now, snapshot.CapturedAt);
    }
}
