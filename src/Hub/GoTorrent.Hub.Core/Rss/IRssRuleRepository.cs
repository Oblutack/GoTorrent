namespace GoTorrent.Hub.Core.Rss;

public interface IRssRuleRepository
{
    Task<IReadOnlyList<RssRule>> GetAllAsync(CancellationToken cancellationToken);

    Task<RssRule?> GetByIdAsync(Guid id, CancellationToken cancellationToken);

    Task AddAsync(RssRule rule, CancellationToken cancellationToken);

    Task UpdateAsync(RssRule rule, CancellationToken cancellationToken);

    /// <summary>Returns false if no rule with that id existed.</summary>
    Task<bool> DeleteAsync(Guid id, CancellationToken cancellationToken);
}
