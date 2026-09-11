namespace GoTorrent.Desktop.Models;

/// <summary>
/// One torrent's list-view shape, mirroring gottrentd's real
/// <c>GET /api/v1/torrents</c> response (<c>internal/api.TorrentSummary</c>
/// on the Go side). Deliberately Desktop's own copy, not shared with
/// <c>GoTorrent.Hub.Core</c> — same "different consumer, no shared
/// package" reasoning already applied between the Go and Hub DTOs.
/// </summary>
public sealed record TorrentSummary(
    string InfoHash,
    string Name,
    string State,
    long Downloaded,
    long Uploaded,
    long Left,
    long TotalLength,
    int NumPieces,
    int HavePieces,
    int PeerCount,
    double SeedRatio,
    bool Private,
    string? Category,
    IReadOnlyList<string>? Tags,
    int QueuePosition,
    bool ForceStart)
{
    public double ProgressFraction => TotalLength == 0 ? 0 : (double)(TotalLength - Left) / TotalLength;
}
