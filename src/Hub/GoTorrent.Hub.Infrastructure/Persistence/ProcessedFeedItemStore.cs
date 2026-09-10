using GoTorrent.Hub.Core.Rss;
using Microsoft.EntityFrameworkCore;

namespace GoTorrent.Hub.Infrastructure.Persistence;

public sealed class ProcessedFeedItemStore(GoTorrentHubDbContext db) : IProcessedFeedItemStore
{
    public Task<bool> IsProcessedAsync(Guid ruleId, string itemKey, CancellationToken cancellationToken) =>
        db.ProcessedFeedItems.AnyAsync(p => p.RuleId == ruleId && p.ItemKey == itemKey, cancellationToken);

    public async Task MarkProcessedAsync(Guid ruleId, string itemKey, CancellationToken cancellationToken)
    {
        db.ProcessedFeedItems.Add(new ProcessedFeedItem { RuleId = ruleId, ItemKey = itemKey });
        try
        {
            await db.SaveChangesAsync(cancellationToken);
        }
        catch (DbUpdateException)
        {
            // The unique (RuleId, ItemKey) index rejected a duplicate -
            // a concurrent poll cycle marking the same item is a benign
            // race (see the DbContext's own doc comment on the index),
            // not a failure worth surfacing to RssPollCycleRunner.
        }
    }
}
