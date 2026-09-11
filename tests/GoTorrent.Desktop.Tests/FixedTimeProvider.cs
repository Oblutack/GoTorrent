namespace GoTorrent.Desktop.Tests;

/// <summary>A controllable clock - same pattern as GoTorrent.Hub.Tests' own FixedTimeProvider, so speed-sample-rate math doesn't depend on real elapsed wall-clock time.</summary>
public sealed class FixedTimeProvider(DateTimeOffset now) : TimeProvider
{
    public DateTimeOffset Now { get; set; } = now;

    public override DateTimeOffset GetUtcNow() => Now;
}
