namespace GoTorrent.Desktop.Models;

/// <summary>
/// The fleet-wide rate limits gottrentd's <c>PATCH /api/v1/session</c>
/// reports back (<c>internal/api.SessionPatchResponse</c>) — the only
/// mutable session-level setting the real API exposes today (ROADMAP's
/// "connection, bandwidth, queue, DHT/PEX/LSD, proxy, scheduler" wishlist
/// is otherwise aspirational; see <c>PatchSessionRequest</c>'s own doc
/// comment on the Go side for why). 0 means unlimited.
/// </summary>
public sealed record SessionLimits(long DownLimitKB, long UpLimitKB);
