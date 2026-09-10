using GoTorrent.Hub.Core.Rss;
using GoTorrent.Hub.Infrastructure.Persistence;

namespace GoTorrent.Hub.Tests.Persistence;

public sealed class RssRuleRepositoryTests : IDisposable
{
    private readonly SqliteDbContextFixture _fixture = new();

    public void Dispose() => _fixture.Dispose();

    private static RssRule MakeRule(string name = "test") => new()
    {
        Name = name,
        FeedUrl = "https://example.com/feed",
        TitlePattern = "^Ubuntu",
        Category = "linux",
    };

    [Fact]
    public async Task AddAsync_ThenGetByIdAsync_RoundTrips()
    {
        using var db = _fixture.CreateContext();
        var repo = new RssRuleRepository(db);
        var rule = MakeRule();

        await repo.AddAsync(rule, CancellationToken.None);

        using var db2 = _fixture.CreateContext();
        var repo2 = new RssRuleRepository(db2);
        var found = await repo2.GetByIdAsync(rule.Id, CancellationToken.None);

        Assert.NotNull(found);
        Assert.Equal(rule.Name, found.Name);
        Assert.Equal(rule.FeedUrl, found.FeedUrl);
        Assert.Equal(rule.Category, found.Category);
    }

    [Fact]
    public async Task GetByIdAsync_UnknownIdReturnsNull()
    {
        using var db = _fixture.CreateContext();
        var repo = new RssRuleRepository(db);

        Assert.Null(await repo.GetByIdAsync(Guid.NewGuid(), CancellationToken.None));
    }

    [Fact]
    public async Task GetAllAsync_ReturnsEveryRuleOrderedByName()
    {
        using var db = _fixture.CreateContext();
        var repo = new RssRuleRepository(db);
        await repo.AddAsync(MakeRule("Zebra"), CancellationToken.None);
        await repo.AddAsync(MakeRule("Alpha"), CancellationToken.None);

        var all = await repo.GetAllAsync(CancellationToken.None);

        Assert.Equal(["Alpha", "Zebra"], all.Select(r => r.Name));
    }

    [Fact]
    public async Task UpdateAsync_PersistsChanges()
    {
        using var db = _fixture.CreateContext();
        var repo = new RssRuleRepository(db);
        var rule = MakeRule();
        await repo.AddAsync(rule, CancellationToken.None);

        rule.Enabled = false;
        rule.Category = "movies";
        await repo.UpdateAsync(rule, CancellationToken.None);

        using var db2 = _fixture.CreateContext();
        var found = await new RssRuleRepository(db2).GetByIdAsync(rule.Id, CancellationToken.None);
        Assert.False(found!.Enabled);
        Assert.Equal("movies", found.Category);
    }

    [Fact]
    public async Task DeleteAsync_RemovesTheRuleAndReturnsTrue()
    {
        using var db = _fixture.CreateContext();
        var repo = new RssRuleRepository(db);
        var rule = MakeRule();
        await repo.AddAsync(rule, CancellationToken.None);

        var deleted = await repo.DeleteAsync(rule.Id, CancellationToken.None);

        Assert.True(deleted);
        Assert.Null(await repo.GetByIdAsync(rule.Id, CancellationToken.None));
    }

    [Fact]
    public async Task DeleteAsync_UnknownIdReturnsFalse()
    {
        using var db = _fixture.CreateContext();
        var repo = new RssRuleRepository(db);

        Assert.False(await repo.DeleteAsync(Guid.NewGuid(), CancellationToken.None));
    }
}
