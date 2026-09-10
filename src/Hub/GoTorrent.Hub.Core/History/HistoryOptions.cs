namespace GoTorrent.Hub.Core.History;

/// <summary>Bound from configuration's "History" section — how often <see cref="HistoryRecorder"/> runs, and how long a <see cref="SessionSnapshot"/> is kept.</summary>
public sealed class HistoryOptions
{
    public const string SectionName = "History";

    public TimeSpan Interval { get; init; } = TimeSpan.FromMinutes(5);

    public TimeSpan SnapshotRetention { get; init; } = TimeSpan.FromDays(30);
}
