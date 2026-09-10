using System.Text.RegularExpressions;

namespace GoTorrent.Hub.Core.Rss;

/// <summary>
/// Decides whether a feed item matches a rule's <see cref="RssRule.TitlePattern"/>
/// — pure domain logic, no I/O, trivially unit-testable without a
/// database or a real feed.
/// </summary>
public static class RssRuleMatcher
{
    // A rule's pattern is user-supplied (via the rules API). An
    // adversarial or simply badly-written pattern (catastrophic
    // backtracking) must not be able to hang a poll cycle - the timeout
    // makes a pathological pattern fail closed (no match) rather than
    // block every other rule behind it.
    private static readonly TimeSpan MatchTimeout = TimeSpan.FromSeconds(1);

    public static bool Matches(RssRule rule, FeedItem item)
    {
        try
        {
            return Regex.IsMatch(item.Title, rule.TitlePattern, RegexOptions.IgnoreCase, MatchTimeout);
        }
        catch (RegexParseException)
        {
            // An invalid pattern matches nothing rather than throwing
            // during a poll cycle - RssRulesController validates a
            // pattern compiles at create/update time, but a rule could in
            // principle still reach here with a bad one (a future direct
            // DB edit, a schema migration path, ...), and one bad rule
            // must never take the whole poll cycle down with it.
            return false;
        }
        catch (RegexMatchTimeoutException)
        {
            return false;
        }
    }
}
