using GoTorrent.Hub.Core.History;
using GoTorrent.Hub.Infrastructure.Persistence;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Infrastructure.History;

public sealed class SessionSnapshotRepository(GoTorrentHubDbContext db) : ISessionSnapshotRepository
{
    public async Task AddAsync(SessionSnapshot snapshot, CancellationToken cancellationToken)
    {
        db.SessionSnapshots.Add(snapshot);
        await db.SaveChangesAsync(cancellationToken);
    }

    public async Task<IReadOnlyList<SessionSnapshot>> GetSinceAsync(DateTimeOffset since, Guid? nodeId, CancellationToken cancellationToken)
    {
        var query = db.SessionSnapshots.AsNoTracking().Where(s => s.CapturedAt >= since);
        if (nodeId is { } id)
        {
            query = query.Where(s => s.NodeId == id);
        }
        return await query.OrderBy(s => s.CapturedAt).ToListAsync(cancellationToken);
    }

    public async Task<int> PruneOlderThanAsync(DateTimeOffset cutoff, CancellationToken cancellationToken) =>
        await db.SessionSnapshots.Where(s => s.CapturedAt < cutoff).ExecuteDeleteAsync(cancellationToken);
}
