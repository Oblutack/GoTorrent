using System.Text.RegularExpressions;
using GoTorrent.Hub.Core.Rss;
using Microsoft.AspNetCore.Mvc;

namespace GoTorrent.Hub.Api.Controllers;

/// <summary>CRUD for RSS auto-download rules — the actual poll cycle lives in RssFeedPollingService.</summary>
[ApiController]
[Route("api/v1/[controller]")]
public sealed class RssRulesController(IRssRuleRepository rules) : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<IReadOnlyList<RssRule>>> ListAsync(CancellationToken cancellationToken) =>
        Ok(await rules.GetAllAsync(cancellationToken));

    [HttpGet("{id:guid}")]
    public async Task<ActionResult<RssRule>> GetAsync(Guid id, CancellationToken cancellationToken)
    {
        var rule = await rules.GetByIdAsync(id, cancellationToken);
        return rule is null ? NotFound() : Ok(rule);
    }

    [HttpPost]
    public async Task<ActionResult<RssRule>> CreateAsync(CreateRssRuleRequest request, CancellationToken cancellationToken)
    {
        if (!IsValidPattern(request.TitlePattern, out var patternError))
        {
            return BadRequest(patternError);
        }

        var rule = new RssRule
        {
            Name = request.Name,
            FeedUrl = request.FeedUrl,
            TitlePattern = request.TitlePattern,
            Category = request.Category,
            DownloadDir = request.DownloadDir,
            Enabled = request.Enabled,
        };
        await rules.AddAsync(rule, cancellationToken);
        // Not nameof(GetAsync): ASP.NET Core strips the "Async" suffix from
        // action names for route/link generation, so the route is actually
        // named "Get" - passing "GetAsync" here makes CreatedAtAction throw
        // "No route matches the supplied values" instead of returning 201.
        return CreatedAtAction("Get", new { id = rule.Id }, rule);
    }

    [HttpPut("{id:guid}")]
    public async Task<IActionResult> UpdateAsync(Guid id, UpdateRssRuleRequest request, CancellationToken cancellationToken)
    {
        var rule = await rules.GetByIdAsync(id, cancellationToken);
        if (rule is null)
        {
            return NotFound();
        }
        if (!IsValidPattern(request.TitlePattern, out var patternError))
        {
            return BadRequest(patternError);
        }

        rule.Name = request.Name;
        rule.FeedUrl = request.FeedUrl;
        rule.TitlePattern = request.TitlePattern;
        rule.Category = request.Category;
        rule.DownloadDir = request.DownloadDir;
        rule.Enabled = request.Enabled;
        await rules.UpdateAsync(rule, cancellationToken);
        return NoContent();
    }

    [HttpDelete("{id:guid}")]
    public async Task<IActionResult> DeleteAsync(Guid id, CancellationToken cancellationToken) =>
        await rules.DeleteAsync(id, cancellationToken) ? NoContent() : NotFound();

    // Rejects an unparseable pattern at write time, rather than letting it
    // reach RssRuleMatcher's own defensive fallback (match nothing) later
    // - a rule author should find out immediately, not discover months
    // later that a typo'd pattern silently never matched anything.
    private static bool IsValidPattern(string pattern, out string? error)
    {
        try
        {
            _ = new Regex(pattern);
            error = null;
            return true;
        }
        catch (ArgumentException ex)
        {
            error = $"'{pattern}' is not a valid regular expression: {ex.Message}";
            return false;
        }
    }
}
