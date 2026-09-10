using GoTorrent.Hub.Core.Rss;

namespace GoTorrent.Hub.Tests.Rss;

/// <summary>In-memory <see cref="IRssRuleRepository"/> for RssPollCycleRunnerTests.</summary>
public sealed class FakeRssRuleRepository : IRssRuleRepository
{
    private readonly List<RssRule> _rules = [];

    public void Seed(RssRule rule) => _rules.Add(rule);

    public Task<IReadOnlyList<RssRule>> GetAllAsync(CancellationToken cancellationToken) =>
        Task.FromResult<IReadOnlyList<RssRule>>([.. _rules]);

    public Task<RssRule?> GetByIdAsync(Guid id, CancellationToken cancellationToken) =>
        Task.FromResult(_rules.FirstOrDefault(r => r.Id == id));

    public Task AddAsync(RssRule rule, CancellationToken cancellationToken)
    {
        _rules.Add(rule);
        return Task.CompletedTask;
    }

    public Task UpdateAsync(RssRule rule, CancellationToken cancellationToken) => Task.CompletedTask;

    public Task<bool> DeleteAsync(Guid id, CancellationToken cancellationToken)
    {
        var removed = _rules.RemoveAll(r => r.Id == id) > 0;
        return Task.FromResult(removed);
    }
}

/// <summary>
/// A fixed set of <see cref="FeedItem"/>s per feed URL, for
/// RssPollCycleRunnerTests - optionally throws to simulate a feed that's
/// temporarily unreachable.
/// </summary>
public sealed class FakeRssFeedReader : IRssFeedReader
{
    private readonly Dictionary<string, IReadOnlyList<FeedItem>> _feeds = [];
    private readonly Dictionary<string, Exception> _failures = [];

    public void SetFeed(string feedUrl, IReadOnlyList<FeedItem> items) => _feeds[feedUrl] = items;

    public void SetFailure(string feedUrl, Exception exception) => _failures[feedUrl] = exception;

    public Task<IReadOnlyList<FeedItem>> ReadAsync(string feedUrl, CancellationToken cancellationToken)
    {
        if (_failures.TryGetValue(feedUrl, out var failure))
        {
            return Task.FromException<IReadOnlyList<FeedItem>>(failure);
        }
        return Task.FromResult(_feeds.TryGetValue(feedUrl, out var items) ? items : []);
    }
}

/// <summary>In-memory <see cref="IProcessedFeedItemStore"/> for RssPollCycleRunnerTests.</summary>
public sealed class FakeProcessedFeedItemStore : IProcessedFeedItemStore
{
    private readonly HashSet<(Guid RuleId, string ItemKey)> _processed = [];

    public IReadOnlyCollection<(Guid RuleId, string ItemKey)> Processed => _processed;

    public Task<bool> IsProcessedAsync(Guid ruleId, string itemKey, CancellationToken cancellationToken) =>
        Task.FromResult(_processed.Contains((ruleId, itemKey)));

    public Task MarkProcessedAsync(Guid ruleId, string itemKey, CancellationToken cancellationToken)
    {
        _processed.Add((ruleId, itemKey));
        return Task.CompletedTask;
    }
}
