namespace GoTorrent.Desktop.Models;

/// <summary>
/// One peer row for the Peers tab's live "contribution" display —
/// derived client-side from two consecutive <see cref="PeerEntry"/>
/// snapshots (gottrentd's <c>GET .../peers</c> reports cumulative
/// Downloaded/Uploaded per peer, not a rate; there is no WS message that
/// streams per-peer stats, so a rate has to come from polling deltas,
/// the same way <c>MainViewModel.RecordSpeedSample</c> turns the WS
/// stream's cumulative <c>sessionStats</c> into a fleet-wide rate).
/// </summary>
public sealed record PeerRow(
    string Addr,
    bool Outbound,
    double DownloadRateKBps,
    double UploadRateKBps,
    double Progress,
    bool AmChoking,
    bool PeerChoking,
    bool AmInterested,
    bool PeerInterested);
