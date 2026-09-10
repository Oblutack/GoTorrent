namespace GoTorrent.Hub.Core.Engine;

/// <summary>
/// The Hub's view of one gottrentd node's control API (Phase 4). Deliberately
/// thin today — just enough to prove the Hub-to-engine seam end to end.
/// gottrentd's full REST surface (add/pause/resume/patch/delete,
/// peers/trackers/pieces, the WebSocket event stream) grows this interface
/// as the Hub actually needs each one, not before: per the design rule in
/// ROADMAP.md's Phase 5 section, the Hub exists to do things the Go engine
/// shouldn't (multi-node aggregation, RSS rules, history, auth) — a
/// pass-through proxy for every engine route would be architecture theater.
/// </summary>
public interface IEngineClient
{
    Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken);

    Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken);
}
