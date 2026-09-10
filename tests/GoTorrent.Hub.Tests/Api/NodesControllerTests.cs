using System.Net;
using System.Net.Http.Json;
using GoTorrent.Hub.Api.Controllers;
using GoTorrent.Hub.Core.Nodes;

namespace GoTorrent.Hub.Tests.Api;

public sealed class NodesControllerTests(GoTorrentHubApiFactory factory) : IClassFixture<GoTorrentHubApiFactory>
{
    private static CreateNodeRequest MakeCreateRequest(string name = "test-node") =>
        new(name, "http://127.0.0.1:6880/", "a-real-looking-token", true);

    [Fact]
    public async Task Create_ThenGet_RoundTrips()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var createResponse = await client.PostAsJsonAsync("/api/v1/nodes", MakeCreateRequest());
        createResponse.EnsureSuccessStatusCode();
        Assert.Equal(HttpStatusCode.Created, createResponse.StatusCode);
        var created = await createResponse.Content.ReadFromJsonAsync<NodeResponse>();

        var getResponse = await client.GetAsync($"/api/v1/nodes/{created!.Id}");
        getResponse.EnsureSuccessStatusCode();
        var fetched = await getResponse.Content.ReadFromJsonAsync<NodeResponse>();

        Assert.Equal(created.Name, fetched!.Name);
        Assert.Equal(created.BaseAddress, fetched.BaseAddress);
    }

    [Fact]
    public async Task Create_ResponseNeverIncludesTheToken()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var createResponse = await client.PostAsJsonAsync("/api/v1/nodes", MakeCreateRequest("no-token-leak"));
        createResponse.EnsureSuccessStatusCode();
        var body = await createResponse.Content.ReadAsStringAsync();

        Assert.DoesNotContain("a-real-looking-token", body);
        Assert.DoesNotContain("\"token\"", body, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public async Task Create_RejectsANonHttpBaseAddress()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.PostAsJsonAsync(
            "/api/v1/nodes", MakeCreateRequest() with { BaseAddress = "not-a-url" });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task Create_RejectsAMissingToken()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.PostAsJsonAsync(
            "/api/v1/nodes", MakeCreateRequest() with { Token = "" });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task Get_UnknownIdReturns404()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.GetAsync($"/api/v1/nodes/{Guid.NewGuid()}");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task List_IncludesACreatedNode()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/nodes", MakeCreateRequest("list-test-node"));
        var created = await createResponse.Content.ReadFromJsonAsync<NodeResponse>();

        var listResponse = await client.GetAsync("/api/v1/nodes");
        listResponse.EnsureSuccessStatusCode();
        var all = await listResponse.Content.ReadFromJsonAsync<List<NodeResponse>>();

        Assert.Contains(all!, n => n.Id == created!.Id);
    }

    [Fact]
    public async Task Update_ThenGet_ReflectsTheChange()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/nodes", MakeCreateRequest("update-test-node"));
        var created = await createResponse.Content.ReadFromJsonAsync<NodeResponse>();

        var updateRequest = new UpdateNodeRequest("renamed-node", created!.BaseAddress, null, false);
        var updateResponse = await client.PutAsJsonAsync($"/api/v1/nodes/{created.Id}", updateRequest);
        Assert.Equal(HttpStatusCode.NoContent, updateResponse.StatusCode);

        var fetched = await (await client.GetAsync($"/api/v1/nodes/{created.Id}")).Content.ReadFromJsonAsync<NodeResponse>();
        Assert.Equal("renamed-node", fetched!.Name);
        Assert.False(fetched.Enabled);
    }

    [Fact]
    public async Task Update_UnknownIdReturns404()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();

        var response = await client.PutAsJsonAsync(
            $"/api/v1/nodes/{Guid.NewGuid()}",
            new UpdateNodeRequest("x", "http://127.0.0.1:6880/", "token", true));

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task Delete_RemovesTheNode()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/nodes", MakeCreateRequest("delete-test-node"));
        var created = await createResponse.Content.ReadFromJsonAsync<NodeResponse>();

        var deleteResponse = await client.DeleteAsync($"/api/v1/nodes/{created!.Id}");
        Assert.Equal(HttpStatusCode.NoContent, deleteResponse.StatusCode);

        var getResponse = await client.GetAsync($"/api/v1/nodes/{created.Id}");
        Assert.Equal(HttpStatusCode.NotFound, getResponse.StatusCode);
    }

    [Fact]
    public async Task GetAggregatedTorrents_IncludesACreatedNodesTorrents()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/nodes", MakeCreateRequest("aggregate-test-node"));
        var created = await createResponse.Content.ReadFromJsonAsync<NodeResponse>();

        var response = await client.GetAsync("/api/v1/nodes/torrents");
        response.EnsureSuccessStatusCode();
        var aggregated = await response.Content.ReadFromJsonAsync<List<AggregatedTorrentSummary>>();

        Assert.Contains(aggregated!, a => a.NodeId == created!.Id && a.Torrent.Name == StubEngineClient.SampleTorrent.Name);
    }

    [Fact]
    public async Task GetStatuses_ReportsACreatedNodeAsReachable()
    {
        using var client = await factory.CreateAuthenticatedClientAsync();
        var createResponse = await client.PostAsJsonAsync("/api/v1/nodes", MakeCreateRequest("status-test-node"));
        var created = await createResponse.Content.ReadFromJsonAsync<NodeResponse>();

        var response = await client.GetAsync("/api/v1/nodes/status");
        response.EnsureSuccessStatusCode();
        var statuses = await response.Content.ReadFromJsonAsync<List<NodeStatus>>();

        var status = statuses!.Single(s => s.NodeId == created!.Id);
        Assert.True(status.Reachable);
        Assert.NotNull(status.Session);
    }
}
