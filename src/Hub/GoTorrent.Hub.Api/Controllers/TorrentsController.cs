using GoTorrent.Hub.Core.Engine;
using Microsoft.AspNetCore.Mvc;

namespace GoTorrent.Hub.Api.Controllers;

/// <summary>
/// Proxies the single configured gottrentd node's torrent list. This is
/// deliberately the plainest possible slice - proof that the Hub-to-engine
/// seam works end to end - not the Hub's actual value proposition: per
/// ROADMAP.md's Phase 5 design rule, a Hub that only forwards every
/// engine route unchanged is architecture theater. Multi-node aggregation,
/// RSS-driven adds, and everything else that justifies this layer (5.2)
/// lands as its own real feature, not retrofitted onto this controller.
/// </summary>
[ApiController]
[Route("api/v1/[controller]")]
public sealed class TorrentsController(IEngineClient engineClient) : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<IReadOnlyList<TorrentSummary>>> ListAsync(CancellationToken cancellationToken)
    {
        var torrents = await engineClient.ListTorrentsAsync(cancellationToken);
        return Ok(torrents);
    }
}
