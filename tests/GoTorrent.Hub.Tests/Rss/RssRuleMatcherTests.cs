using GoTorrent.Hub.Core.Rss;

namespace GoTorrent.Hub.Tests.Rss;

public sealed class RssRuleMatcherTests
{
    private static RssRule MakeRule(string pattern) => new()
    {
        Name = "test",
        FeedUrl = "https://example.com/feed",
        TitlePattern = pattern,
    };

    [Fact]
    public void Matches_ReturnsTrueForAMatchingTitle()
    {
        var rule = MakeRule(@"^Ubuntu.*\.iso$");
        var item = new FeedItem("Ubuntu 26.04.iso", "magnet:?xt=urn:btih:abc", "guid-1", null);

        Assert.True(RssRuleMatcher.Matches(rule, item));
    }

    [Fact]
    public void Matches_ReturnsFalseForANonMatchingTitle()
    {
        var rule = MakeRule(@"^Ubuntu.*\.iso$");
        var item = new FeedItem("Debian 13.iso", "magnet:?xt=urn:btih:abc", "guid-1", null);

        Assert.False(RssRuleMatcher.Matches(rule, item));
    }

    [Fact]
    public void Matches_IsCaseInsensitive()
    {
        var rule = MakeRule("ubuntu");
        var item = new FeedItem("UBUNTU 26.04", null, "guid-1", null);

        Assert.True(RssRuleMatcher.Matches(rule, item));
    }

    [Fact]
    public void Matches_ReturnsFalseRatherThanThrowingForAnInvalidPattern()
    {
        var rule = MakeRule("(unterminated[");
        var item = new FeedItem("anything", null, "guid-1", null);

        Assert.False(RssRuleMatcher.Matches(rule, item));
    }

    [Fact]
    public void Matches_ReturnsFalseRatherThanHangingOnCatastrophicBacktracking()
    {
        // A classic ReDoS pattern - (a+)+ against a long run of a's with
        // no trailing match forces exponential backtracking. This must
        // fail closed within the configured timeout, not hang the test
        // (or a real poll cycle).
        var rule = MakeRule("^(a+)+$");
        var item = new FeedItem(new string('a', 40) + "!", null, "guid-1", null);

        Assert.False(RssRuleMatcher.Matches(rule, item));
    }
}
