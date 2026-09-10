namespace GoTorrent.Hub.Core.Rss;

/// <summary>
/// A record that a specific feed item, for a specific rule, has already
/// been dealt with — matched (or not) and, if matched, already handed to
/// the engine — so a later poll of the same feed never re-adds it. Scoped
/// per rule, not globally per item, since the same feed item could
/// legitimately match two different rules with different categories/save
/// paths.
/// </summary>
public sealed class ProcessedFeedItem
{
    public Guid Id { get; init; } = Guid.NewGuid();

    public required Guid RuleId { get; init; }

    /// <summary>See <see cref="FeedItem.Key"/>.</summary>
    public required string ItemKey { get; init; }

    public DateTimeOffset ProcessedAt { get; init; } = DateTimeOffset.UtcNow;
}
