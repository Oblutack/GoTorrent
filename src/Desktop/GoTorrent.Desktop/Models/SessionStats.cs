namespace GoTorrent.Desktop.Models;

/// <summary>A fleet-wide rollup, mirroring gottrentd's <c>GET /api/v1/session</c> response.</summary>
public sealed record SessionStats(
    int TorrentCount,
    int DownloadingCount,
    int SeedingCount,
    int PausedCount,
    int ErrorCount,
    long TotalDownloaded,
    long TotalUploaded,
    int TotalPeerCount);
