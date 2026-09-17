namespace GoTorrent.Desktop.Models;

/// <summary>
/// A previewed .torrent's file list, mirroring gottrentd's real
/// <c>POST /api/v1/torrents/preview</c> response
/// (<c>internal/api.PreviewResponse</c>). No magnet case on the Go side -
/// a magnet has no file list until peers supply metadata - so this is
/// only ever reachable for a real uploaded .torrent file or a URL.
/// </summary>
public sealed record PreviewResponse(
    string InfoHash,
    string Name,
    long TotalLength,
    long PieceLength,
    bool Private,
    IReadOnlyList<PreviewFile> Files);

public sealed record PreviewFile(IReadOnlyList<string> Path, long Length);
