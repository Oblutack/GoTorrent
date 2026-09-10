using GoTorrent.Hub.Core.History;
using Microsoft.AspNetCore.Mvc;

namespace GoTorrent.Hub.Api.Controllers;

/// <summary>
/// Read-only: every entry here comes from <see cref="HistoryRecorder"/>,
/// not a caller - there is nothing to create/update/delete through this
/// controller, unlike RssRulesController/NodesController.
/// </summary>
[ApiController]
[Route("api/v1/[controller]")]
public sealed class HistoryController(ITorrentHistoryRepository history, ISessionSnapshotRepository snapshots) : ControllerBase
{
    [HttpGet("completed")]
    public async Task<ActionResult<IReadOnlyList<TorrentHistoryEntry>>> GetCompletedAsync(
        [FromQuery] int take, CancellationToken cancellationToken)
    {
        take = take <= 0 ? 100 : take;
        if (take > 1000)
        {
            return BadRequest("take must be 1000 or fewer.");
        }
        return Ok(await history.GetRecentAsync(take, cancellationToken));
    }

    [HttpGet("summary")]
    public async Task<ActionResult<HistorySummary>> GetSummaryAsync(CancellationToken cancellationToken) =>
        Ok(await history.GetSummaryAsync(cancellationToken));

    [HttpGet("snapshots")]
    public async Task<ActionResult<IReadOnlyList<SessionSnapshot>>> GetSnapshotsAsync(
        [FromQuery] DateTimeOffset? since, [FromQuery] Guid? nodeId, CancellationToken cancellationToken)
    {
        // Defaults to the last 7 days rather than "everything" - a
        // caller building a timeline graph almost always wants a bounded
        // window, and PruneOlderThanAsync means "everything" isn't even
        // a stable concept past HistoryOptions.SnapshotRetention anyway.
        var effectiveSince = since ?? DateTimeOffset.UtcNow.AddDays(-7);
        return Ok(await snapshots.GetSinceAsync(effectiveSince, nodeId, cancellationToken));
    }
}
