using GoTorrent.Hub.Core.History;
using GoTorrent.Hub.Infrastructure.Persistence;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Infrastructure.History;

public sealed class TorrentHistoryRepository(GoTorrentHubDbContext db) : ITorrentHistoryRepository
{
    public Task<bool> ExistsAsync(Guid nodeId, string infoHash, CancellationToken cancellationToken) =>
        db.TorrentHistoryEntries.AnyAsync(h => h.NodeId == nodeId && h.InfoHash == infoHash, cancellationToken);

    public async Task AddAsync(TorrentHistoryEntry entry, CancellationToken cancellationToken)
    {
        db.TorrentHistoryEntries.Add(entry);
        try
        {
            await db.SaveChangesAsync(cancellationToken);
        }
        catch (DbUpdateException)
        {
            // The unique (NodeId, InfoHash) index rejected a duplicate -
            // a concurrent recording pass racing itself (see the
            // DbContext's own doc comment on the index) is benign, not a
            // failure worth surfacing to HistoryRecorder.
        }
    }

    public async Task<IReadOnlyList<TorrentHistoryEntry>> GetRecentAsync(int take, CancellationToken cancellationToken) =>
        await db.TorrentHistoryEntries.AsNoTracking()
            .OrderByDescending(h => h.CompletedAt)
            .Take(take)
            .ToListAsync(cancellationToken);

    public async Task<HistorySummary> GetSummaryAsync(CancellationToken cancellationToken)
    {
        // A single DB-side aggregate query, not "pull every row and sum
        // in memory" - the archive is meant to grow forever, unlike
        // SessionSnapshots.
        var count = await db.TorrentHistoryEntries.CountAsync(cancellationToken);
        if (count == 0)
        {
            return new HistorySummary(0, 0, 0);
        }
        var totalDownloaded = await db.TorrentHistoryEntries.SumAsync(h => h.Downloaded, cancellationToken);
        var totalUploaded = await db.TorrentHistoryEntries.SumAsync(h => h.Uploaded, cancellationToken);
        return new HistorySummary(count, totalDownloaded, totalUploaded);
    }
}
