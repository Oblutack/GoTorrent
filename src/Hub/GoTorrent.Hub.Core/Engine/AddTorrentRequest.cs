namespace GoTorrent.Hub.Core.Engine;

/// <summary>
/// A request to add a torrent to a gottrentd node, mirroring the JSON body
/// <c>POST /api/v1/torrents</c> accepts on the Go side (see
/// <c>internal/api.AddRequest</c>) for the magnet/URL cases — file upload
/// is a separate multipart path the Go API also supports, but nothing on
/// the Hub side needs it yet (RSS items are always a magnet link or a
/// direct <c>.torrent</c> URL, never a local file to upload).
/// </summary>
/// <remarks>
/// Exactly one of <paramref name="Magnet"/> or <paramref name="Url"/>
/// should be set — the Go side rejects a request with neither, and this
/// client does not attempt to validate that itself before sending, so the
/// same honest failure surfaces however this is called.
/// </remarks>
public sealed record AddTorrentRequest(
    string? Magnet,
    string? Url,
    string? Category,
    IReadOnlyList<string>? Tags,
    string? DownloadDir);
