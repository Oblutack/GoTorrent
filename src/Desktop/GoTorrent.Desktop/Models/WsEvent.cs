namespace GoTorrent.Desktop.Models;

/// <summary>
/// One message off gottrentd's real <c>GET /api/v1/events</c> WebSocket
/// stream (<c>internal/api.WSEvent</c>). <see cref="Kind"/> is one of
/// "torrentAdded"/"torrentRemoved"/"torrentStateChanged"/"peerConnected"/
/// "peerDisconnected"/"pieceVerified"/"sessionStats" — a plain string, not
/// an enum, for the same "the Go side might add a new kind before this
/// client knows about it" reasoning <c>TorrentSummary.State</c> already
/// uses. Only the fields relevant to <see cref="Kind"/> are populated.
/// </summary>
public sealed record WsEvent(
    string Kind,
    DateTimeOffset Time,
    string? InfoHash,
    string? State,
    string? PeerAddr,
    int? PieceIndex,
    SessionStats? Session);
