namespace GoTorrent.Hub.Core.Rss;

public interface IRssFeedReader
{
    Task<IReadOnlyList<FeedItem>> ReadAsync(string feedUrl, CancellationToken cancellationToken);
}
