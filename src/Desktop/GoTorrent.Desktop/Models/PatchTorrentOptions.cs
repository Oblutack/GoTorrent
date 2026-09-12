namespace GoTorrent.Desktop.Models;

/// <summary>
/// <c>PATCH /api/v1/torrents/{hash}</c>'s body (<c>internal/api.PatchTorrentRequest</c>
/// on the Go side) - every field nullable, so "leave alone" (the field is
/// absent from the JSON entirely) is distinguishable from "explicitly set
/// to the zero value".
/// </summary>
public sealed record PatchTorrentOptions(
    string? Category = null,
    IReadOnlyList<string>? Tags = null,
    int? QueuePosition = null,
    bool? ForceStart = null,
    bool? Sequential = null,
    long? DownLimitKB = null,
    long? UpLimitKB = null,
    string? DownloadDir = null);
