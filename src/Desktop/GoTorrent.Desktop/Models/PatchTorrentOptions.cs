namespace GoTorrent.Desktop.Models;

/// <summary>
/// <c>PATCH /api/v1/torrents/{hash}</c>'s body (<c>internal/api.PatchTorrentRequest</c>
/// on the Go side) - every field nullable, so "leave alone" (the field is
/// absent from the JSON entirely) is distinguishable from "explicitly set
/// to the zero value". Only the fields this app's UI actually writes are
/// included here - the Go side's request also supports a rate-limit and
/// a download-dir move, neither wired up on the Desktop side yet.
/// </summary>
public sealed record PatchTorrentOptions(
    string? Category = null,
    IReadOnlyList<string>? Tags = null,
    int? QueuePosition = null,
    bool? ForceStart = null,
    bool? Sequential = null);
