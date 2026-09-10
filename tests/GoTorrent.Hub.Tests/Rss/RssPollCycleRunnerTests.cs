using GoTorrent.Hub.Core.Engine;
using GoTorrent.Hub.Core.Rss;
using GoTorrent.Hub.Tests.Api;
using Microsoft.Extensions.Logging.Abstractions;

namespace GoTorrent.Hub.Tests.Rss;

public sealed class RssPollCycleRunnerTests
{
    private static RssRule MakeRule(string pattern, bool enabled = true) => new()
    {
        Name = "test-rule",
        FeedUrl = "https://example.com/feed",
        TitlePattern = pattern,
        Category = "movies",
        Enabled = enabled,
    };

    private static (FakeRssRuleRepository Rules, FakeRssFeedReader Feed, FakeProcessedFeedItemStore Processed, StubEngineClient Engine, RssPollCycleRunner Runner)
        Build()
    {
        var rules = new FakeRssRuleRepository();
        var feed = new FakeRssFeedReader();
        var processed = new FakeProcessedFeedItemStore();
        var engine = new StubEngineClient();
        var runner = new RssPollCycleRunner(rules, feed, processed, engine, NullLogger<RssPollCycleRunner>.Instance);
        return (rules, feed, processed, engine, runner);
    }

    [Fact]
    public async Task RunAsync_AddsAMatchingUnprocessedItem()
    {
        var (rules, feed, processed, engine, runner) = Build();
        var rule = MakeRule("^Ubuntu");
        rules.Seed(rule);
        feed.SetFeed(rule.FeedUrl, [new FeedItem("Ubuntu 26.04.iso", "magnet:?xt=urn:btih:abc", "guid-1", null)]);

        await runner.RunAsync(CancellationToken.None);

        var added = Assert.Single(engine.AddedTorrents);
        Assert.Equal("magnet:?xt=urn:btih:abc", added.Magnet);
        Assert.Null(added.Url);
        Assert.Equal("movies", added.Category);
        Assert.Contains((rule.Id, "guid-1"), processed.Processed);
    }

    [Fact]
    public async Task RunAsync_UsesUrlNotMagnetWhenTheLinkIsNotAMagnetUri()
    {
        var (rules, feed, _, engine, runner) = Build();
        var rule = MakeRule("^Ubuntu");
        rules.Seed(rule);
        feed.SetFeed(rule.FeedUrl, [new FeedItem("Ubuntu 26.04.iso", "https://example.com/ubuntu.torrent", "guid-1", null)]);

        await runner.RunAsync(CancellationToken.None);

        var added = Assert.Single(engine.AddedTorrents);
        Assert.Null(added.Magnet);
        Assert.Equal("https://example.com/ubuntu.torrent", added.Url);
    }

    [Fact]
    public async Task RunAsync_SkipsNonMatchingItems()
    {
        var (rules, feed, _, engine, runner) = Build();
        var rule = MakeRule("^Ubuntu");
        rules.Seed(rule);
        feed.SetFeed(rule.FeedUrl, [new FeedItem("Debian 13.iso", "magnet:?xt=urn:btih:abc", "guid-1", null)]);

        await runner.RunAsync(CancellationToken.None);

        Assert.Empty(engine.AddedTorrents);
    }

    [Fact]
    public async Task RunAsync_SkipsDisabledRules()
    {
        var (rules, feed, _, engine, runner) = Build();
        var rule = MakeRule("^Ubuntu", enabled: false);
        rules.Seed(rule);
        feed.SetFeed(rule.FeedUrl, [new FeedItem("Ubuntu 26.04.iso", "magnet:?xt=urn:btih:abc", "guid-1", null)]);

        await runner.RunAsync(CancellationToken.None);

        Assert.Empty(engine.AddedTorrents);
    }

    [Fact]
    public async Task RunAsync_SkipsAlreadyProcessedItemsAndDoesNotReAdd()
    {
        var (rules, feed, processed, engine, runner) = Build();
        var rule = MakeRule("^Ubuntu");
        rules.Seed(rule);
        feed.SetFeed(rule.FeedUrl, [new FeedItem("Ubuntu 26.04.iso", "magnet:?xt=urn:btih:abc", "guid-1", null)]);
        await processed.MarkProcessedAsync(rule.Id, "guid-1", CancellationToken.None);

        await runner.RunAsync(CancellationToken.None);

        Assert.Empty(engine.AddedTorrents);
    }

    [Fact]
    public async Task RunAsync_MarksProcessedEvenWhenTheEngineReportsADuplicate()
    {
        var (rules, feed, processed, engine, runner) = Build();
        var rule = MakeRule("^Ubuntu");
        rules.Seed(rule);
        feed.SetFeed(rule.FeedUrl, [new FeedItem("Ubuntu 26.04.iso", "magnet:?xt=urn:btih:abc", "guid-1", null)]);
        engine.AddTorrentFailure = new EngineDuplicateTorrentException("already added");

        await runner.RunAsync(CancellationToken.None);

        Assert.Contains((rule.Id, "guid-1"), processed.Processed);
    }

    [Fact]
    public async Task RunAsync_DoesNotMarkProcessedOnATransientEngineFailure()
    {
        var (rules, feed, processed, engine, runner) = Build();
        var rule = MakeRule("^Ubuntu");
        rules.Seed(rule);
        feed.SetFeed(rule.FeedUrl, [new FeedItem("Ubuntu 26.04.iso", "magnet:?xt=urn:btih:abc", "guid-1", null)]);
        engine.AddTorrentFailure = new HttpRequestException("engine unreachable");

        await runner.RunAsync(CancellationToken.None);

        Assert.DoesNotContain((rule.Id, "guid-1"), processed.Processed);
    }

    [Fact]
    public async Task RunAsync_ContinuesToOtherRulesWhenOneFeedIsUnreachable()
    {
        var (rules, feed, _, engine, runner) = Build();
        var brokenRule = new RssRule { Name = "broken", FeedUrl = "https://dead.example.com/feed", TitlePattern = "x" };
        var healthyRule = MakeRule("^Ubuntu");
        rules.Seed(brokenRule);
        rules.Seed(healthyRule);
        feed.SetFailure(brokenRule.FeedUrl, new HttpRequestException("feed unreachable"));
        feed.SetFeed(healthyRule.FeedUrl, [new FeedItem("Ubuntu 26.04.iso", "magnet:?xt=urn:btih:abc", "guid-1", null)]);

        await runner.RunAsync(CancellationToken.None);

        var added = Assert.Single(engine.AddedTorrents);
        Assert.Equal("magnet:?xt=urn:btih:abc", added.Magnet);
    }
}
