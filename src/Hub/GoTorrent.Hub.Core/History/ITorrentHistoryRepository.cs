namespace GoTorrent.Hub.Core.History;

public interface ITorrentHistoryRepository
{
    Task<bool> ExistsAsync(Guid nodeId, string infoHash, CancellationToken cancellationToken);

    /// <summary>
    /// Adds an entry. A duplicate <c>(NodeId, InfoHash)</c> — a concurrent
    /// recording pass racing itself — is treated as benign rather than
    /// thrown, the same reasoning as RSS's <c>ProcessedFeedItemStore</c>.
    /// </summary>
    Task AddAsync(TorrentHistoryEntry entry, CancellationToken cancellationToken);

    Task<IReadOnlyList<TorrentHistoryEntry>> GetRecentAsync(int take, CancellationToken cancellationToken);

    Task<HistorySummary> GetSummaryAsync(CancellationToken cancellationToken);
}
