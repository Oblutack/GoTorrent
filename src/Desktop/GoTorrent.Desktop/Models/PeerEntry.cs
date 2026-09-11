namespace GoTorrent.Desktop.Models;

/// <summary>
/// One connected peer, mirroring gottrentd's real
/// <c>GET /api/v1/torrents/{hash}/peers</c> response
/// (<c>internal/api.PeerEntry</c>).
/// </summary>
public sealed record PeerEntry(
    string Addr,
    bool Outbound,
    long Downloaded,
    long Uploaded,
    bool AmChoking,
    bool AmInterested,
    bool PeerChoking,
    bool PeerInterested,
    double Progress);
