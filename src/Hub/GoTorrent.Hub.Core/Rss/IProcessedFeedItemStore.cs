namespace GoTorrent.Hub.Core.Rss;

public interface IProcessedFeedItemStore
{
    Task<bool> IsProcessedAsync(Guid ruleId, string itemKey, CancellationToken cancellationToken);

    Task MarkProcessedAsync(Guid ruleId, string itemKey, CancellationToken cancellationToken);
}
