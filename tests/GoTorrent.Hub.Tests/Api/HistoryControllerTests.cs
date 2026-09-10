using System.Net.Http.Json;
using GoTorrent.Hub.Core.History;
using Microsoft.Extensions.DependencyInjection;

namespace GoTorrent.Hub.Tests.Api;

/// <summary>
/// HistoryController is read-only - nothing comes in through it, only
/// out - so unlike RssRulesControllerTests/NodesControllerTests, seeding
/// here goes straight through the real repositories resolved from the
/// factory's own DI container rather than through HTTP, since there is
/// no HTTP write path to seed through. The controller/routing/DI wiring
/// this class actually verifies is entirely on the read side.
/// </summary>
public sealed class HistoryControllerTests(GoTorrentHubApiFactory factory) : IClassFixture<GoTorrentHubApiFactory>
{
    private async Task SeedCompletedAsync(TorrentHistoryEntry entry)
    {
        using var scope = factory.Services.CreateScope();
        var history = scope.ServiceProvider.GetRequiredService<ITorrentHistoryRepository>();
        await history.AddAsync(entry, CancellationToken.None);
    }

    private async Task SeedSnapshotAsync(SessionSnapshot snapshot)
    {
        using var scope = factory.Services.CreateScope();
        var snapshots = scope.ServiceProvider.GetRequiredService<ISessionSnapshotRepository>();
        await snapshots.AddAsync(snapshot, CancellationToken.None);
    }

    [Fact]
    public async Task GetCompleted_ReturnsASeededEntry()
    {
        var nodeId = Guid.NewGuid();
        await SeedCompletedAsync(new TorrentHistoryEntry
        {
            NodeId = nodeId,
            NodeName = "node-a",
            InfoHash = "history-controller-test-hash",
            Name = "history-controller-test.iso",
            Downloaded = 123,
            Uploaded = 456,
        });
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/api/v1/history/completed");
        response.EnsureSuccessStatusCode();
        var entries = await response.Content.ReadFromJsonAsync<List<TorrentHistoryEntry>>();

        Assert.Contains(entries!, e => e.NodeId == nodeId && e.Name == "history-controller-test.iso");
    }

    [Fact]
    public async Task GetCompleted_RejectsATakeAboveTheLimit()
    {
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/api/v1/history/completed?take=5000");

        Assert.Equal(System.Net.HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task GetSummary_ReflectsSeededEntries()
    {
        await SeedCompletedAsync(new TorrentHistoryEntry
        {
            NodeId = Guid.NewGuid(),
            NodeName = "node-summary",
            InfoHash = "summary-test-hash",
            Name = "summary-test.iso",
            Downloaded = 1000,
            Uploaded = 2000,
        });
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/api/v1/history/summary");
        response.EnsureSuccessStatusCode();
        var summary = await response.Content.ReadFromJsonAsync<HistorySummary>();

        Assert.True(summary!.CompletedCount >= 1);
        Assert.True(summary.TotalDownloaded >= 1000);
    }

    [Fact]
    public async Task GetSnapshots_ReturnsASeededSnapshotWithinTheWindow()
    {
        var nodeId = Guid.NewGuid();
        await SeedSnapshotAsync(new SessionSnapshot
        {
            NodeId = nodeId,
            NodeName = "node-a",
            CapturedAt = DateTimeOffset.UtcNow,
            TorrentCount = 3,
        });
        using var client = factory.CreateClient();

        var response = await client.GetAsync($"/api/v1/history/snapshots?nodeId={nodeId}");
        response.EnsureSuccessStatusCode();
        var snapshots = await response.Content.ReadFromJsonAsync<List<SessionSnapshot>>();

        var snapshot = Assert.Single(snapshots!);
        Assert.Equal(nodeId, snapshot.NodeId);
        Assert.Equal(3, snapshot.TorrentCount);
    }

    [Fact]
    public async Task GetSnapshots_ExcludesSnapshotsOutsideTheDefaultWindow()
    {
        var nodeId = Guid.NewGuid();
        await SeedSnapshotAsync(new SessionSnapshot
        {
            NodeId = nodeId,
            NodeName = "node-old",
            CapturedAt = DateTimeOffset.UtcNow.AddDays(-30),
        });
        using var client = factory.CreateClient();

        var response = await client.GetAsync($"/api/v1/history/snapshots?nodeId={nodeId}");
        response.EnsureSuccessStatusCode();
        var snapshots = await response.Content.ReadFromJsonAsync<List<SessionSnapshot>>();

        Assert.Empty(snapshots!);
    }
}
