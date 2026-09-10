using System.Net;
using System.Net.Http.Json;
using GoTorrent.Hub.Api.Controllers;
using GoTorrent.Hub.Core.Rss;

namespace GoTorrent.Hub.Tests.Api;

public sealed class RssRulesControllerTests(GoTorrentHubApiFactory factory)
    : IClassFixture<GoTorrentHubApiFactory>
{
    private static CreateRssRuleRequest MakeCreateRequest(string name = "test-rule") =>
        new(name, "https://example.com/feed", "^Ubuntu", "linux", null, true);

    [Fact]
    public async Task Create_ThenGet_RoundTrips()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var createResponse = await client.PostAsJsonAsync("/api/v1/rssrules", MakeCreateRequest());
        createResponse.EnsureSuccessStatusCode();
        Assert.Equal(HttpStatusCode.Created, createResponse.StatusCode);
        var created = await createResponse.Content.ReadFromJsonAsync<RssRule>();

        var getResponse = await client.GetAsync($"/api/v1/rssrules/{created!.Id}");
        getResponse.EnsureSuccessStatusCode();
        var fetched = await getResponse.Content.ReadFromJsonAsync<RssRule>();

        Assert.Equal(created.Name, fetched!.Name);
        Assert.Equal(created.FeedUrl, fetched.FeedUrl);
    }

    [Fact]
    public async Task Create_RejectsAnInvalidRegexPattern()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.PostAsJsonAsync(
            "/api/v1/rssrules", MakeCreateRequest() with { TitlePattern = "(unterminated[" });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task Get_UnknownIdReturns404()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.GetAsync($"/api/v1/rssrules/{Guid.NewGuid()}");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task List_IncludesACreatedRule()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/rssrules", MakeCreateRequest("list-test-rule"));
        var created = await createResponse.Content.ReadFromJsonAsync<RssRule>();

        var listResponse = await client.GetAsync("/api/v1/rssrules");
        listResponse.EnsureSuccessStatusCode();
        var all = await listResponse.Content.ReadFromJsonAsync<List<RssRule>>();

        Assert.Contains(all!, r => r.Id == created!.Id);
    }

    [Fact]
    public async Task Update_ThenGet_ReflectsTheChange()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/rssrules", MakeCreateRequest("update-test-rule"));
        var created = await createResponse.Content.ReadFromJsonAsync<RssRule>();

        var updateRequest = new UpdateRssRuleRequest("renamed", created!.FeedUrl, created.TitlePattern, "movies", null, false);
        var updateResponse = await client.PutAsJsonAsync($"/api/v1/rssrules/{created.Id}", updateRequest);
        Assert.Equal(HttpStatusCode.NoContent, updateResponse.StatusCode);

        var fetched = await (await client.GetAsync($"/api/v1/rssrules/{created.Id}")).Content.ReadFromJsonAsync<RssRule>();
        Assert.Equal("renamed", fetched!.Name);
        Assert.Equal("movies", fetched.Category);
        Assert.False(fetched.Enabled);
    }

    [Fact]
    public async Task Update_UnknownIdReturns404()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.PutAsJsonAsync(
            $"/api/v1/rssrules/{Guid.NewGuid()}",
            new UpdateRssRuleRequest("x", "https://example.com/feed", "^x", null, null, true));

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task Delete_RemovesTheRule()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/rssrules", MakeCreateRequest("delete-test-rule"));
        var created = await createResponse.Content.ReadFromJsonAsync<RssRule>();

        var deleteResponse = await client.DeleteAsync($"/api/v1/rssrules/{created!.Id}");
        Assert.Equal(HttpStatusCode.NoContent, deleteResponse.StatusCode);

        var getResponse = await client.GetAsync($"/api/v1/rssrules/{created.Id}");
        Assert.Equal(HttpStatusCode.NotFound, getResponse.StatusCode);
    }
}
