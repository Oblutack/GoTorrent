using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>A scripted <see cref="IUpdateChecker"/> - no real network call in tests.</summary>
public sealed class FakeUpdateChecker : IUpdateChecker
{
    public string? LatestTag { get; set; }

    public Task<string?> GetLatestVersionTagAsync(CancellationToken cancellationToken) => Task.FromResult(LatestTag);
}
