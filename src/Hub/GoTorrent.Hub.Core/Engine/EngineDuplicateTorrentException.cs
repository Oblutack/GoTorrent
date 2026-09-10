namespace GoTorrent.Hub.Core.Engine;

/// <summary>
/// Thrown by <see cref="IEngineClient.AddTorrentAsync"/> when gottrentd
/// reports 409 Conflict — the infohash is already managed by that node.
/// Its own type rather than a generic HTTP failure because callers (the
/// RSS poller, in particular) need to treat "already added" as an
/// expected, non-error outcome: a feed item matching a rule twice across
/// polls is normal, not a fault.
/// </summary>
public sealed class EngineDuplicateTorrentException(string message) : Exception(message);
