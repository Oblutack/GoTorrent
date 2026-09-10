namespace GoTorrent.Hub.Core.Nodes;

public interface IEngineNodeRepository
{
    Task<IReadOnlyList<EngineNode>> GetAllAsync(CancellationToken cancellationToken);

    Task<EngineNode?> GetByIdAsync(Guid id, CancellationToken cancellationToken);

    Task AddAsync(EngineNode node, CancellationToken cancellationToken);

    Task UpdateAsync(EngineNode node, CancellationToken cancellationToken);

    /// <summary>Returns false if no node with that id existed.</summary>
    Task<bool> DeleteAsync(Guid id, CancellationToken cancellationToken);
}
