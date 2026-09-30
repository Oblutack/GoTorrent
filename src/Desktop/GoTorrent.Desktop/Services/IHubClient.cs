using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// The Hub-side counterpart to <see cref="IEngineClient"/> - a much
/// smaller surface, since Desktop only ever needs the Hub for one thing
/// (activity history; see 6.5 Stage 6's "per-torrent notes, and activity
/// history" item), not the full control-plane surface gottrentd itself
/// exposes. A completely separate, optional connection from the primary
/// gottrentd one - Desktop talks directly to gottrentd for everything
/// else (see the architecture note on <see cref="EngineClient"/>), and
/// the Hub is an optional second endpoint layered on top purely for the
/// cross-process archive gottrentd itself has no reason to keep.
/// </summary>
public interface IHubClient
{
    /// <summary>
    /// Exchanges a username/password for a real JWT, against the Hub's
    /// unauthenticated <c>/api/v1/auth/login</c> route - called on a
    /// client constructed with an empty <see cref="HubOptions.Token"/>,
    /// since there is no token yet at this point.
    /// </summary>
    Task<HubLoginResult> LoginAsync(string userName, string password, CancellationToken cancellationToken);

    Task<IReadOnlyList<ActivityHistoryEntry>> GetCompletedAsync(int take, CancellationToken cancellationToken);

    Task<ActivityHistorySummary> GetSummaryAsync(CancellationToken cancellationToken);
}
