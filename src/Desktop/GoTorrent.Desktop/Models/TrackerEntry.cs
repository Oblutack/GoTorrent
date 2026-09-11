namespace GoTorrent.Desktop.Models;

/// <summary>
/// One tracker's most recent announce result, mirroring gottrentd's real
/// <c>GET /api/v1/torrents/{hash}/trackers</c> response
/// (<c>internal/api.TrackerEntry</c>).
/// </summary>
public sealed record TrackerEntry(
    string Url,
    DateTimeOffset LastAnnounce,
    string? LastError,
    int Seeders,
    int Leechers);
