namespace GoTorrent.Hub.Api.Controllers;

/// <summary>Request bodies for <see cref="RssRulesController"/>'s create/update routes.</summary>
public sealed record CreateRssRuleRequest(
    string Name,
    string FeedUrl,
    string TitlePattern,
    string? Category,
    string? DownloadDir,
    bool Enabled = true);

public sealed record UpdateRssRuleRequest(
    string Name,
    string FeedUrl,
    string TitlePattern,
    string? Category,
    string? DownloadDir,
    bool Enabled);
