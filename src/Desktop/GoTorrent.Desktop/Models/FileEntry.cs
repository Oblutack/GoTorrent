namespace GoTorrent.Desktop.Models;

/// <summary>
/// One file of a torrent's file tree, mirroring gottrentd's real
/// <c>GET /api/v1/torrents/{hash}/files</c> response
/// (<c>internal/api.FileEntry</c>). <see cref="Priority"/> is gottrentd's
/// lowercase name ("skip"/"low"/"normal"/"high") — read-only here, since
/// there is no per-file priority PATCH route on the Go side yet.
/// </summary>
public sealed record FileEntry(IReadOnlyList<string> Path, long Length, string Priority)
{
    public string DisplayPath => string.Join('/', Path);
}
