namespace GoTorrent.Hub.Core.Rss;

/// <summary>
/// Bound from configuration's "RssPolling" section — how often the
/// background poller runs a full <see cref="RssPollCycleRunner"/> pass.
/// </summary>
public sealed class RssPollingOptions
{
    public const string SectionName = "RssPolling";

    public TimeSpan Interval { get; init; } = TimeSpan.FromMinutes(15);
}
