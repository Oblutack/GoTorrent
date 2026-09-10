using GoTorrent.Hub.Core.Rss;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Infrastructure.Persistence;

public sealed class RssRuleRepository(GoTorrentHubDbContext db) : IRssRuleRepository
{
    public async Task<IReadOnlyList<RssRule>> GetAllAsync(CancellationToken cancellationToken) =>
        await db.RssRules.AsNoTracking().OrderBy(r => r.Name).ToListAsync(cancellationToken);

    public Task<RssRule?> GetByIdAsync(Guid id, CancellationToken cancellationToken) =>
        db.RssRules.FirstOrDefaultAsync(r => r.Id == id, cancellationToken);

    public async Task AddAsync(RssRule rule, CancellationToken cancellationToken)
    {
        db.RssRules.Add(rule);
        await db.SaveChangesAsync(cancellationToken);
    }

    public async Task UpdateAsync(RssRule rule, CancellationToken cancellationToken)
    {
        db.RssRules.Update(rule);
        await db.SaveChangesAsync(cancellationToken);
    }

    public async Task<bool> DeleteAsync(Guid id, CancellationToken cancellationToken)
    {
        var rule = await db.RssRules.FindAsync([id], cancellationToken);
        if (rule is null)
        {
            return false;
        }
        db.RssRules.Remove(rule);
        await db.SaveChangesAsync(cancellationToken);
        return true;
    }
}
