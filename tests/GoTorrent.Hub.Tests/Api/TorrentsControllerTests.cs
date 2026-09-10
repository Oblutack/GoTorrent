using System.Net;
using System.Net.Http.Json;
using GoTorrent.Hub.Core.Engine;

namespace GoTorrent.Hub.Tests.Api;

public sealed class TorrentsControllerTests(GoTorrentHubApiFactory factory)
    : IClassFixture<GoTorrentHubApiFactory>
{
    [Fact]
    public async Task Get_ReturnsTheStubbedEngineClientsTorrents()
    {
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/api/v1/torrents");

        response.EnsureSuccessStatusCode();
        var torrents = await response.Content.ReadFromJsonAsync<List<TorrentSummary>>();
        var torrent = Assert.Single(torrents!);
        Assert.Equal(StubEngineClient.SampleTorrent.InfoHash, torrent.InfoHash);
    }
}

public sealed class SessionControllerTests(GoTorrentHubApiFactory factory)
    : IClassFixture<GoTorrentHubApiFactory>
{
    [Fact]
    public async Task Get_ReturnsTheStubbedEngineClientsSession()
    {
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/api/v1/session");

        response.EnsureSuccessStatusCode();
        var session = await response.Content.ReadFromJsonAsync<SessionStats>();
        Assert.Equal(StubEngineClient.SampleSession.TorrentCount, session!.TorrentCount);
    }
}

public sealed class HealthCheckTests(GoTorrentHubApiFactory factory)
    : IClassFixture<GoTorrentHubApiFactory>
{
    [Fact]
    public async Task Health_ReportsHealthyWhenTheEngineClientAnswers()
    {
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/health");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }
}
