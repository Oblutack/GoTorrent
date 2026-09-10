using GoTorrent.Hub.Core.History;
using GoTorrent.Hub.Infrastructure.History;

namespace GoTorrent.Hub.Tests.Persistence;

public sealed class TorrentHistoryRepositoryTests : IDisposable
{
    private readonly SqliteDbContextFixture _fixture = new();

    public void Dispose() => _fixture.Dispose();

    private static TorrentHistoryEntry MakeEntry(Guid nodeId, string infoHash = "aaaa", string name = "test.iso") => new()
    {
        NodeId = nodeId,
        NodeName = "node-a",
        InfoHash = infoHash,
        Name = name,
        Category = "linux",
        TotalLength = 1000,
        Downloaded = 1000,
        Uploaded = 2000,
        SeedRatio = 2.0,
    };

    [Fact]
    public async Task AddAsync_ThenExistsAsync_ReturnsTrue()
    {
        using var db = _fixture.CreateContext();
        var repo = new TorrentHistoryRepository(db);
        var nodeId = Guid.NewGuid();

        await repo.AddAsync(MakeEntry(nodeId), CancellationToken.None);

        using var db2 = _fixture.CreateContext();
        Assert.True(await new TorrentHistoryRepository(db2).ExistsAsync(nodeId, "aaaa", CancellationToken.None));
    }

    [Fact]
    public async Task ExistsAsync_IsScopedPerNode()
    {
        using var db = _fixture.CreateContext();
        var repo = new TorrentHistoryRepository(db);
        var nodeA = Guid.NewGuid();
        var nodeB = Guid.NewGuid();

        await repo.AddAsync(MakeEntry(nodeA), CancellationToken.None);

        Assert.True(await repo.ExistsAsync(nodeA, "aaaa", CancellationToken.None));
        Assert.False(await repo.ExistsAsync(nodeB, "aaaa", CancellationToken.None));
    }

    [Fact]
    public async Task AddAsync_TwiceForTheSameNodeAndInfoHashDoesNotThrow()
    {
        using var db = _fixture.CreateContext();
        var repo = new TorrentHistoryRepository(db);
        var nodeId = Guid.NewGuid();

        await repo.AddAsync(MakeEntry(nodeId), CancellationToken.None);
        // The unique (NodeId, InfoHash) index would reject this as a raw
        // insert - proving AddAsync actually swallows that
        // DbUpdateException rather than letting it propagate.
        await repo.AddAsync(MakeEntry(nodeId), CancellationToken.None);

        var summary = await repo.GetSummaryAsync(CancellationToken.None);
        Assert.Equal(1, summary.CompletedCount);
    }

    [Fact]
    public async Task GetRecentAsync_ReturnsNewestFirst()
    {
        using var db = _fixture.CreateContext();
        var repo = new TorrentHistoryRepository(db);
        var nodeId = Guid.NewGuid();
        var older = new TorrentHistoryEntry
        {
            NodeId = nodeId,
            NodeName = "node-a",
            InfoHash = "aaaa",
            Name = "older.iso",
            TotalLength = 1,
            CompletedAt = DateTimeOffset.UtcNow.AddHours(-2),
        };
        var newer = new TorrentHistoryEntry
        {
            NodeId = nodeId,
            NodeName = "node-a",
            InfoHash = "bbbb",
            Name = "newer.iso",
            TotalLength = 1,
            CompletedAt = DateTimeOffset.UtcNow,
        };
        await repo.AddAsync(older, CancellationToken.None);
        await repo.AddAsync(newer, CancellationToken.None);

        var recent = await repo.GetRecentAsync(10, CancellationToken.None);

        Assert.Equal(["newer.iso", "older.iso"], recent.Select(e => e.Name));
    }

    [Fact]
    public async Task GetSummaryAsync_SumsAcrossEntries()
    {
        using var db = _fixture.CreateContext();
        var repo = new TorrentHistoryRepository(db);
        await repo.AddAsync(new TorrentHistoryEntry { NodeId = Guid.NewGuid(), NodeName = "a", InfoHash = "aaaa", Name = "x", Downloaded = 100, Uploaded = 50 }, CancellationToken.None);
        await repo.AddAsync(new TorrentHistoryEntry { NodeId = Guid.NewGuid(), NodeName = "b", InfoHash = "bbbb", Name = "y", Downloaded = 200, Uploaded = 150 }, CancellationToken.None);

        var summary = await repo.GetSummaryAsync(CancellationToken.None);

        Assert.Equal(2, summary.CompletedCount);
        Assert.Equal(300, summary.TotalDownloaded);
        Assert.Equal(200, summary.TotalUploaded);
    }

    [Fact]
    public async Task GetSummaryAsync_OnEmptyArchiveReturnsZeroes()
    {
        using var db = _fixture.CreateContext();
        var repo = new TorrentHistoryRepository(db);

        var summary = await repo.GetSummaryAsync(CancellationToken.None);

        Assert.Equal(new HistorySummary(0, 0, 0), summary);
    }
}
