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
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.GetAsync("/api/v1/torrents");

        response.EnsureSuccessStatusCode();
        var torrents = await response.Content.ReadFromJsonAsync<List<TorrentSummary>>();
        var torrent = Assert.Single(torrents!);
        Assert.Equal(StubEngineClient.SampleTorrent.InfoHash, torrent.InfoHash);
    }

    [Fact]
    public async Task Get_WithNoBearerTokenReturns401()
    {
        // Every controller but AuthController's own routes requires
        // authorization by default now (Program.cs's
        // MapControllers().RequireAuthorization()) - this is the
        // regression test for that, using a plain unauthenticated
        // client rather than assuming every other passing test proves it
        // (they'd all pass identically if authorization silently
        // stopped being enforced).
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/api/v1/torrents");

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }
}

public sealed class SessionControllerTests(GoTorrentHubApiFactory factory)
    : IClassFixture<GoTorrentHubApiFactory>
{
    [Fact]
    public async Task Get_ReturnsTheStubbedEngineClientsSession()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

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
        // Deliberately the plain, unauthenticated client - /health is a
        // separate route from MapControllers() entirely and is meant to
        // stay reachable without a bearer token (see Program.cs's own
        // comment on why). This test is the regression check for that
        // staying true.
        using var client = factory.CreateClient();

        var response = await client.GetAsync("/health");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }
}
