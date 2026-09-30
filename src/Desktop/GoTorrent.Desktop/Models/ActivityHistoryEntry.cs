namespace GoTorrent.Desktop.Models;

/// <summary>
/// One completed torrent from the Hub's activity archive, mirroring
/// <c>GET /api/v1/history/completed</c>'s real response shape
/// (<c>GoTorrent.Hub.Core.History.TorrentHistoryEntry</c> on the Hub
/// side). Deliberately Desktop's own copy, not shared code - the same "a
/// public API response is a contract, not an internal type" reasoning
/// already applied between gottrentd's <see cref="TorrentSummary"/> and
/// its own Go-side DTO.
/// </summary>
public sealed record ActivityHistoryEntry(
    Guid Id,
    Guid NodeId,
    string NodeName,
    string InfoHash,
    string Name,
    string? Category,
    long TotalLength,
    long Downloaded,
    long Uploaded,
    double SeedRatio,
    DateTimeOffset CompletedAt);
