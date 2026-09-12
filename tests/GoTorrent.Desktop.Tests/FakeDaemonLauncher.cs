using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>An in-memory <see cref="IDaemonLauncher"/> - no real process spawned in tests.</summary>
public sealed class FakeDaemonLauncher : IDaemonLauncher
{
    public bool IsAvailable { get; set; } = true;

    public bool IsRunning { get; private set; }

    public string? ExistingToken { get; set; }

    public string? TokenToReturnOnStart { get; set; }

    public string? LastStartApiAddress { get; private set; }

    public int StartCallCount { get; private set; }

    public int StopCallCount { get; private set; }

    public string? TryReadExistingToken() => ExistingToken;

    public Task<string?> StartAsync(string apiAddress, CancellationToken cancellationToken)
    {
        StartCallCount++;
        LastStartApiAddress = apiAddress;
        if (TokenToReturnOnStart is null)
        {
            return Task.FromResult<string?>(null);
        }
        IsRunning = true;
        return Task.FromResult<string?>(TokenToReturnOnStart);
    }

    public void Stop()
    {
        StopCallCount++;
        IsRunning = false;
    }
}
