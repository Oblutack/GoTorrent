namespace GoTorrent.Hub.Core.Rss;

/// <summary>
/// A saved "watch this feed, auto-download anything matching this
/// pattern" rule (ROADMAP.md's 5.2 RSS auto-download rules). Persisted via
/// <see cref="IRssRuleRepository"/>; matching against a feed item is pure
/// logic with no dependency on how a rule got loaded — see
/// <see cref="RssRuleMatcher"/>.
/// </summary>
public sealed class RssRule
{
    public Guid Id { get; init; } = Guid.NewGuid();

    public required string Name { get; set; }

    public required string FeedUrl { get; set; }

    /// <summary>
    /// A .NET regular expression matched against each feed item's title,
    /// case-insensitively. Validated for well-formedness where a rule is
    /// created or updated (see RssRulesController) — an already-saved rule
    /// whose pattern somehow fails to compile at match time is treated as
    /// matching nothing rather than aborting the whole poll cycle; see
    /// RssRuleMatcher's own doc comment.
    /// </summary>
    public required string TitlePattern { get; set; }

    /// <summary>Category to add matching torrents under, if any.</summary>
    public string? Category { get; set; }

    /// <summary>Save path override for matching torrents, if any.</summary>
    public string? DownloadDir { get; set; }

    public bool Enabled { get; set; } = true;

    public DateTimeOffset CreatedAt { get; init; } = DateTimeOffset.UtcNow;
}
