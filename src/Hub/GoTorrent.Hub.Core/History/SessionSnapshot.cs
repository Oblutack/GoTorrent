namespace GoTorrent.Hub.Core.History;

/// <summary>
/// One node's <see cref="Engine.SessionStats"/>, captured at a point in
/// time — the raw material for a download/upload timeline graph.
/// Recorded on every <see cref="HistoryRecorder"/> pass for every
/// reachable enabled node, pruned after
/// <see cref="HistoryOptions.SnapshotRetention"/> so this table doesn't
/// grow unbounded forever; unlike <see cref="TorrentHistoryEntry"/>, a
/// snapshot has no lasting archival value on its own once it's outside
/// the window anything actually graphs.
/// </summary>
public sealed class SessionSnapshot
{
    public Guid Id { get; init; } = Guid.NewGuid();

    public required Guid NodeId { get; init; }

    public required string NodeName { get; init; }

    public DateTimeOffset CapturedAt { get; init; } = DateTimeOffset.UtcNow;

    public int TorrentCount { get; init; }

    public int DownloadingCount { get; init; }

    public int SeedingCount { get; init; }

    public long TotalDownloaded { get; init; }

    public long TotalUploaded { get; init; }

    public int TotalPeerCount { get; init; }
}
