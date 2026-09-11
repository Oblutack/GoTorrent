namespace GoTorrent.Desktop.Models;

/// <summary>
/// The single-torrent view, mirroring gottrentd's real
/// <c>GET /api/v1/torrents/{hash}</c> response
/// (<c>internal/api.TorrentDetail</c>) — <c>TorrentSummary</c>'s fields
/// flattened in (Go embeds it inline with no JSON tag), plus the fields
/// not worth carrying on every entry of the list view.
/// </summary>
public sealed record TorrentDetail(
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
    bool ForceStart,
    string Source,
    string DownloadDir,
    string? ContentPath,
    bool InEndgame,
    double SeedingDurationSeconds)
{
    public double ProgressFraction => TotalLength == 0 ? 0 : (double)(TotalLength - Left) / TotalLength;
}
