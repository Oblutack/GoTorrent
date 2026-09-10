using GoTorrent.Hub.Core.History;

namespace GoTorrent.Hub.Tests.History;

/// <summary>In-memory <see cref="ITorrentHistoryRepository"/> for HistoryRecorderTests - mirrors the real repository's dedup-by-(NodeId, InfoHash) behavior, not just a bare list.</summary>
public sealed class FakeTorrentHistoryRepository : ITorrentHistoryRepository
{
    private readonly List<TorrentHistoryEntry> _entries = [];

    public IReadOnlyList<TorrentHistoryEntry> Entries => _entries;

    public Task<bool> ExistsAsync(Guid nodeId, string infoHash, CancellationToken cancellationToken) =>
        Task.FromResult(_entries.Any(e => e.NodeId == nodeId && e.InfoHash == infoHash));

    public Task AddAsync(TorrentHistoryEntry entry, CancellationToken cancellationToken)
    {
        if (!_entries.Any(e => e.NodeId == entry.NodeId && e.InfoHash == entry.InfoHash))
        {
            _entries.Add(entry);
        }
        return Task.CompletedTask;
    }

    public Task<IReadOnlyList<TorrentHistoryEntry>> GetRecentAsync(int take, CancellationToken cancellationToken) =>
        Task.FromResult<IReadOnlyList<TorrentHistoryEntry>>([.. _entries.OrderByDescending(e => e.CompletedAt).Take(take)]);

    public Task<HistorySummary> GetSummaryAsync(CancellationToken cancellationToken) =>
        Task.FromResult(new HistorySummary(_entries.Count, _entries.Sum(e => e.Downloaded), _entries.Sum(e => e.Uploaded)));
}

/// <summary>In-memory <see cref="ISessionSnapshotRepository"/> for HistoryRecorderTests - real prune-by-cutoff behavior, not a stub.</summary>
public sealed class FakeSessionSnapshotRepository : ISessionSnapshotRepository
{
    private readonly List<SessionSnapshot> _snapshots = [];

    public IReadOnlyList<SessionSnapshot> Snapshots => _snapshots;

    public Task AddAsync(SessionSnapshot snapshot, CancellationToken cancellationToken)
    {
        _snapshots.Add(snapshot);
        return Task.CompletedTask;
    }

    public Task<IReadOnlyList<SessionSnapshot>> GetSinceAsync(DateTimeOffset since, Guid? nodeId, CancellationToken cancellationToken)
    {
        var query = _snapshots.Where(s => s.CapturedAt >= since);
        if (nodeId is { } id)
        {
            query = query.Where(s => s.NodeId == id);
        }
        return Task.FromResult<IReadOnlyList<SessionSnapshot>>([.. query.OrderBy(s => s.CapturedAt)]);
    }

    public Task<int> PruneOlderThanAsync(DateTimeOffset cutoff, CancellationToken cancellationToken)
    {
        var removed = _snapshots.RemoveAll(s => s.CapturedAt < cutoff);
        return Task.FromResult(removed);
    }
}

/// <summary>A <see cref="TimeProvider"/> whose "now" is set explicitly, for deterministic CapturedAt/CompletedAt/retention-cutoff assertions.</summary>
public sealed class FixedTimeProvider(DateTimeOffset now) : TimeProvider
{
    public DateTimeOffset Now { get; set; } = now;

    public override DateTimeOffset GetUtcNow() => Now;
}
