namespace GoTorrent.Hub.Core.History;

public interface ISessionSnapshotRepository
{
    Task AddAsync(SessionSnapshot snapshot, CancellationToken cancellationToken);

    /// <summary>Snapshots at or after <paramref name="since"/>, optionally scoped to one node, oldest first (graph-ready order).</summary>
    Task<IReadOnlyList<SessionSnapshot>> GetSinceAsync(DateTimeOffset since, Guid? nodeId, CancellationToken cancellationToken);

    /// <summary>Deletes every snapshot older than <paramref name="cutoff"/>. Returns how many were removed.</summary>
    Task<int> PruneOlderThanAsync(DateTimeOffset cutoff, CancellationToken cancellationToken);
}
