using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>A controllable <see cref="IHubClient"/> for MainViewModelTests - no real Hub or HTTP involved.</summary>
public sealed class FakeHubClient : IHubClient
{
    public Exception? Failure { get; set; }

    public HubLoginResult LoginResult { get; set; } = new("fake-jwt", DateTimeOffset.UtcNow.AddDays(1));

    public string? LastLoginUserName { get; private set; }
    public string? LastLoginPassword { get; private set; }

    public List<ActivityHistoryEntry> Completed { get; set; } = [];

    public ActivityHistorySummary Summary { get; set; } = new(0, 0, 0);

    /// <summary>How many times <see cref="GetCompletedAsync"/> has actually been called - what a re-load test checks.</summary>
    public int GetCompletedCallCount { get; private set; }

    public Task<HubLoginResult> LoginAsync(string userName, string password, CancellationToken cancellationToken)
    {
        LastLoginUserName = userName;
        LastLoginPassword = password;
        return Failure is not null ? Task.FromException<HubLoginResult>(Failure) : Task.FromResult(LoginResult);
    }

    public Task<IReadOnlyList<ActivityHistoryEntry>> GetCompletedAsync(int take, CancellationToken cancellationToken)
    {
        GetCompletedCallCount++;
        return Failure is not null
            ? Task.FromException<IReadOnlyList<ActivityHistoryEntry>>(Failure)
            : Task.FromResult<IReadOnlyList<ActivityHistoryEntry>>(Completed);
    }

    public Task<ActivityHistorySummary> GetSummaryAsync(CancellationToken cancellationToken) =>
        Failure is not null ? Task.FromException<ActivityHistorySummary>(Failure) : Task.FromResult(Summary);
}
