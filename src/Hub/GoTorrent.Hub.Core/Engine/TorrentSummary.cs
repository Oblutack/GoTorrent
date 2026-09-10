namespace GoTorrent.Hub.Core.Engine;

/// <summary>
/// One torrent's list-view shape, mirroring gottrentd's
/// <c>GET /api/v1/torrents</c> response (see the Go side's
/// <c>internal/api.TorrentSummary</c>). Kept as its own type here rather
/// than shared code with the Go project (different language, no shared
/// package) — deliberately narrow, matching only what the Go API actually
/// promises, so a field the engine adds later doesn't silently disappear
/// mid-deserialization; it's simply ignored until this record grows to
/// match.
/// </summary>
/// <remarks>
/// State is a string, not an enum, on purpose: gottrentd's own
/// <c>torrent.State</c> can in principle gain a new value on the Go side
/// before this client is updated to know about it, and a string never
/// throws on an unrecognized value the way <see cref="System.Text.Json"/>'s
/// strict enum converter would.
/// </remarks>
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
    bool ForceStart);
