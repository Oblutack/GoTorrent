namespace GoTorrent.Hub.Core.Rss;

/// <summary>One entry read from an RSS/Atom feed — not persisted itself.</summary>
public sealed record FeedItem(string Title, string? Link, string? Guid, DateTimeOffset? PublishedAt)
{
    /// <summary>
    /// A stable identifier for duplicate suppression: the feed's own item
    /// GUID if it provided one (the correct choice — a feed is free to
    /// change an item's title or link later without it being a new item),
    /// falling back to the link, and finally the title if a feed somehow
    /// gives neither.
    /// </summary>
    public string Key => Guid ?? Link ?? Title;
}
