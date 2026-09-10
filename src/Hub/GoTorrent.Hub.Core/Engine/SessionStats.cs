namespace GoTorrent.Hub.Core.Engine;

/// <summary>
/// A fleet-wide rollup, mirroring gottrentd's <c>GET /api/v1/session</c>
/// response (the Go side's <c>internal/api.SessionStats</c>).
/// </summary>
public sealed record SessionStats(
    int TorrentCount,
    int DownloadingCount,
    int SeedingCount,
    int PausedCount,
    int ErrorCount,
    long TotalDownloaded,
    long TotalUploaded,
    int TotalPeerCount);
