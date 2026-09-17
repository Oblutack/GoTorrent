namespace GoTorrent.Desktop.Models;

/// <summary>
/// Free disk space at an arbitrary path, mirroring gottrentd's real
/// <c>GET /api/v1/diskspace</c> response (<c>internal/api.DiskSpaceResponse</c>) -
/// Stage 6's disk-space guard needs this for whatever custom save path the
/// user picked in the Add Torrent dialog. Desktop's own <c>SessionStats</c>
/// DTO was never grown to include Stage 5's own <c>freeDiskBytes</c> field
/// (nothing in Desktop consumed the rest of that daemon-info work either -
/// there's no status bar for it yet), and it wouldn't help here anyway:
/// it only ever covers the fleet's own configured default download
/// directory, not an arbitrary path a user might type or browse to.
/// </summary>
public sealed record DiskSpaceResponse(string Path, long FreeBytes);
