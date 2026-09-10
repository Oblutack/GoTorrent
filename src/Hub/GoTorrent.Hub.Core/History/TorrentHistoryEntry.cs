namespace GoTorrent.Hub.Core.History;

/// <summary>
/// One torrent that finished downloading on one node — the "completed-
/// torrent archive that outlives the engine process" ROADMAP.md's 5.2
/// calls for. Recorded once, the first time <see cref="HistoryRecorder"/>
/// observes a torrent with nothing left to download; deleting the
/// torrent from gottrentd (or the node itself from the Hub) afterward
/// does not touch this record — that is the entire point of an archive.
/// </summary>
/// <remarks>
/// <see cref="NodeName"/> is a deliberate snapshot, not a live join
/// against <see cref="Nodes.EngineNode"/>: a node can be renamed or
/// deleted long after a torrent completed on it, and a historical record
/// should read the way things were at completion time, not silently
/// change (or go blank) because of something unrelated happening later.
/// </remarks>
public sealed class TorrentHistoryEntry
{
    public Guid Id { get; init; } = Guid.NewGuid();

    public required Guid NodeId { get; init; }

    public required string NodeName { get; init; }

    public required string InfoHash { get; init; }

    public required string Name { get; init; }

    public string? Category { get; init; }

    public long TotalLength { get; init; }

    public long Downloaded { get; init; }

    public long Uploaded { get; init; }

    public double SeedRatio { get; init; }

    public DateTimeOffset CompletedAt { get; init; } = DateTimeOffset.UtcNow;
}
