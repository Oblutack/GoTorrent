using GoTorrent.Hub.Core.Engine;
using Microsoft.AspNetCore.Mvc;

namespace GoTorrent.Hub.Api.Controllers;

/// <summary>Proxies the single configured gottrentd node's session stats.</summary>
[ApiController]
[Route("api/v1/[controller]")]
public sealed class SessionController(IEngineClient engineClient) : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<SessionStats>> GetAsync(CancellationToken cancellationToken)
    {
        var stats = await engineClient.GetSessionAsync(cancellationToken);
        return Ok(stats);
    }
}
