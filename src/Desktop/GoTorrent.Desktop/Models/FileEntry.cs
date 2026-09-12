namespace GoTorrent.Desktop.Models;

/// <summary>
/// One file of a torrent's file tree, mirroring gottrentd's real
/// <c>GET /api/v1/torrents/{hash}/files</c> response
/// (<c>internal/api.FileEntry</c>). <see cref="Priority"/> is gottrentd's
/// lowercase name ("skip"/"low"/"normal"/"high") — changing it goes through
/// <see cref="IEngineClient.SetFilePriorityAsync"/>, which PATCHes
/// <c>/api/v1/torrents/{hash}/files/{index}</c>.
/// </summary>
public sealed record FileEntry(IReadOnlyList<string> Path, long Length, string Priority)
{
    public string DisplayPath => string.Join('/', Path);
}
