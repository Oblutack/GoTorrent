using GoTorrent.Hub.Infrastructure.Persistence;

namespace GoTorrent.Hub.Tests.Persistence;

public sealed class ProcessedFeedItemStoreTests : IDisposable
{
    private readonly SqliteDbContextFixture _fixture = new();

    public void Dispose() => _fixture.Dispose();

    [Fact]
    public async Task IsProcessedAsync_FalseBeforeMarking()
    {
        using var db = _fixture.CreateContext();
        var store = new ProcessedFeedItemStore(db);

        Assert.False(await store.IsProcessedAsync(Guid.NewGuid(), "item-1", CancellationToken.None));
    }

    [Fact]
    public async Task MarkProcessedAsync_ThenIsProcessedAsync_ReturnsTrue()
    {
        var ruleId = Guid.NewGuid();
        using var db = _fixture.CreateContext();
        var store = new ProcessedFeedItemStore(db);

        await store.MarkProcessedAsync(ruleId, "item-1", CancellationToken.None);

        using var db2 = _fixture.CreateContext();
        Assert.True(await new ProcessedFeedItemStore(db2).IsProcessedAsync(ruleId, "item-1", CancellationToken.None));
    }

    [Fact]
    public async Task MarkProcessedAsync_IsScopedPerRule()
    {
        var ruleA = Guid.NewGuid();
        var ruleB = Guid.NewGuid();
        using var db = _fixture.CreateContext();
        var store = new ProcessedFeedItemStore(db);

        await store.MarkProcessedAsync(ruleA, "item-1", CancellationToken.None);

        Assert.True(await store.IsProcessedAsync(ruleA, "item-1", CancellationToken.None));
        Assert.False(await store.IsProcessedAsync(ruleB, "item-1", CancellationToken.None));
    }

    [Fact]
    public async Task MarkProcessedAsync_TwiceForTheSameItemDoesNotThrow()
    {
        var ruleId = Guid.NewGuid();
        using var db = _fixture.CreateContext();
        var store = new ProcessedFeedItemStore(db);

        await store.MarkProcessedAsync(ruleId, "item-1", CancellationToken.None);
        // The unique (RuleId, ItemKey) index would reject this as a raw
        // insert - proving MarkProcessedAsync actually swallows that
        // DbUpdateException rather than letting it propagate.
        await store.MarkProcessedAsync(ruleId, "item-1", CancellationToken.None);

        Assert.True(await store.IsProcessedAsync(ruleId, "item-1", CancellationToken.None));
    }
}
