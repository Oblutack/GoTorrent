using GoTorrent.Hub.Core.Nodes;
using GoTorrent.Hub.Infrastructure.Persistence;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Infrastructure.Nodes;

public sealed class EngineNodeRepository(GoTorrentHubDbContext db) : IEngineNodeRepository
{
    public async Task<IReadOnlyList<EngineNode>> GetAllAsync(CancellationToken cancellationToken) =>
        await db.EngineNodes.AsNoTracking().OrderBy(n => n.Name).ToListAsync(cancellationToken);

    public Task<EngineNode?> GetByIdAsync(Guid id, CancellationToken cancellationToken) =>
        db.EngineNodes.FirstOrDefaultAsync(n => n.Id == id, cancellationToken);

    public async Task AddAsync(EngineNode node, CancellationToken cancellationToken)
    {
        db.EngineNodes.Add(node);
        await db.SaveChangesAsync(cancellationToken);
    }

    public async Task UpdateAsync(EngineNode node, CancellationToken cancellationToken)
    {
        db.EngineNodes.Update(node);
        await db.SaveChangesAsync(cancellationToken);
    }

    public async Task<bool> DeleteAsync(Guid id, CancellationToken cancellationToken)
    {
        var node = await db.EngineNodes.FindAsync([id], cancellationToken);
        if (node is null)
        {
            return false;
        }
        db.EngineNodes.Remove(node);
        await db.SaveChangesAsync(cancellationToken);
        return true;
    }
}
