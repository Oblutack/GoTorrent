using GoTorrent.Hub.Core.Nodes;
using Microsoft.AspNetCore.Mvc;

namespace GoTorrent.Hub.Api.Controllers;

/// <summary>
/// CRUD for registered gottrentd nodes, plus the two aggregate views that
/// are the actual point of registering more than one (ROADMAP.md's 5.2 —
/// "this is the moment the Hub stops being a proxy and becomes a
/// gateway"): <see cref="GetAggregatedTorrentsAsync"/> (every enabled
/// node's torrents, tagged by node) and <see cref="GetStatusesAsync"/>
/// (every node's reachability, disabled ones included). The actual
/// fan-out/failure-tolerance logic lives in
/// <see cref="NodeAggregationService"/>, not here.
/// </summary>
[ApiController]
[Route("api/v1/[controller]")]
public sealed class NodesController(IEngineNodeRepository nodes, NodeAggregationService aggregation) : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<IReadOnlyList<NodeResponse>>> ListAsync(CancellationToken cancellationToken)
    {
        var all = await nodes.GetAllAsync(cancellationToken);
        return Ok(all.Select(ToResponse));
    }

    [HttpGet("{id:guid}")]
    public async Task<ActionResult<NodeResponse>> GetAsync(Guid id, CancellationToken cancellationToken)
    {
        var node = await nodes.GetByIdAsync(id, cancellationToken);
        return node is null ? NotFound() : Ok(ToResponse(node));
    }

    [HttpPost]
    public async Task<ActionResult<NodeResponse>> CreateAsync(CreateNodeRequest request, CancellationToken cancellationToken)
    {
        if (!TryParseBaseAddress(request.BaseAddress, out var baseAddress, out var addressError))
        {
            return BadRequest(addressError);
        }
        if (string.IsNullOrWhiteSpace(request.Token))
        {
            return BadRequest("Token is required.");
        }

        var node = new EngineNode
        {
            Name = request.Name,
            BaseAddress = baseAddress,
            Token = request.Token,
            Enabled = request.Enabled,
        };
        await nodes.AddAsync(node, cancellationToken);
        // Not nameof(GetAsync): ASP.NET Core strips the "Async" suffix
        // from action names for route/link generation, so the route is
        // actually named "Get" - see RssRulesController's own comment on
        // this exact trap, hit and fixed there first.
        return CreatedAtAction("Get", new { id = node.Id }, ToResponse(node));
    }

    [HttpPut("{id:guid}")]
    public async Task<IActionResult> UpdateAsync(Guid id, UpdateNodeRequest request, CancellationToken cancellationToken)
    {
        var node = await nodes.GetByIdAsync(id, cancellationToken);
        if (node is null)
        {
            return NotFound();
        }
        if (!TryParseBaseAddress(request.BaseAddress, out var baseAddress, out var addressError))
        {
            return BadRequest(addressError);
        }

        node.Name = request.Name;
        node.BaseAddress = baseAddress;
        node.Enabled = request.Enabled;
        if (!string.IsNullOrWhiteSpace(request.Token))
        {
            node.Token = request.Token;
        }
        await nodes.UpdateAsync(node, cancellationToken);
        return NoContent();
    }

    [HttpDelete("{id:guid}")]
    public async Task<IActionResult> DeleteAsync(Guid id, CancellationToken cancellationToken) =>
        await nodes.DeleteAsync(id, cancellationToken) ? NoContent() : NotFound();

    [HttpGet("torrents")]
    public async Task<ActionResult<IReadOnlyList<AggregatedTorrentSummary>>> GetAggregatedTorrentsAsync(CancellationToken cancellationToken) =>
        Ok(await aggregation.GetAggregatedTorrentsAsync(cancellationToken));

    [HttpGet("status")]
    public async Task<ActionResult<IReadOnlyList<NodeStatus>>> GetStatusesAsync(CancellationToken cancellationToken) =>
        Ok(await aggregation.GetNodeStatusesAsync(cancellationToken));

    private static NodeResponse ToResponse(EngineNode node) =>
        new(node.Id, node.Name, node.BaseAddress.ToString(), node.Enabled, node.CreatedAt);

    private static bool TryParseBaseAddress(string value, out Uri baseAddress, out string? error)
    {
        if (Uri.TryCreate(value, UriKind.Absolute, out var uri) &&
            (uri.Scheme == Uri.UriSchemeHttp || uri.Scheme == Uri.UriSchemeHttps))
        {
            baseAddress = uri;
            error = null;
            return true;
        }
        baseAddress = null!;
        error = $"'{value}' is not a valid absolute http:// or https:// URL.";
        return false;
    }
}
