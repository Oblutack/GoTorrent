using System.Text.Json;
using GoTorrent.Hub.Core.Events;
using Microsoft.AspNetCore.Http.Connections;
using Microsoft.AspNetCore.SignalR.Client;
using Microsoft.Extensions.DependencyInjection;

namespace GoTorrent.Hub.Tests.Api;

/// <summary>
/// A real <see cref="HubConnection"/> against the real hosted
/// <c>GoTorrentEventsHub</c>, not just unit coverage of
/// <c>NodeEventFanOutCoordinator</c> with fakes - proves the JWT-over-
/// query-string wiring (Program.cs's <c>OnMessageReceived</c>, needed
/// since a browser can't set a header on a WS handshake) and the
/// broadcast path (<c>INodeEventBroadcaster</c> → the Hub → a connected
/// client) both actually work together. Uses the LongPolling transport
/// rather than WebSockets: <c>WebApplicationFactory</c>'s in-memory
/// <c>TestServer</c> has no real socket for <c>ClientWebSocket</c> to
/// connect to, and the transport a client
/// picks is a client-side choice this project's own server code doesn't
/// (and shouldn't) care about - <c>WebSocketNodeEventStreamTests</c>
/// already covers the real WebSocket transport, on the other side of
/// this same fan-out feature (Hub-to-gottrentd, not client-to-Hub).
/// </summary>
public sealed class GoTorrentEventsHubTests(GoTorrentHubApiFactory factory) : IClassFixture<GoTorrentHubApiFactory>
{
    private HubConnection BuildConnection(string? accessToken) =>
        new HubConnectionBuilder()
            .WithUrl(new Uri(factory.Server.BaseAddress, "/hubs/events"), HttpTransportType.LongPolling, options =>
            {
                options.HttpMessageHandlerFactory = _ => factory.Server.CreateHandler();
                if (accessToken is not null)
                {
                    options.AccessTokenProvider = () => Task.FromResult<string?>(accessToken);
                }
            })
            .Build();

    [Fact]
    public async Task Connect_WithAValidToken_ReceivesABroadcastEvent()
    {
        using var httpClient = await factory.CreateAuthenticatedClientAsync();
        var token = httpClient.DefaultRequestHeaders.Authorization!.Parameter!;

        await using var connection = BuildConnection(token);
        var received = new TaskCompletionSource<NodeEventEnvelope>(TaskCreationOptions.RunContinuationsAsynchronously);
        connection.On<NodeEventEnvelope>("NodeEvent", envelope => received.TrySetResult(envelope));
        await connection.StartAsync();

        using var scope = factory.Services.CreateScope();
        var broadcaster = scope.ServiceProvider.GetRequiredService<INodeEventBroadcaster>();
        var sent = new NodeEventEnvelope(
            Guid.NewGuid(), "hub-test-node", JsonDocument.Parse("""{"kind":"torrentAdded"}""").RootElement.Clone());
        await broadcaster.BroadcastAsync(sent, CancellationToken.None);

        var completed = await Task.WhenAny(received.Task, Task.Delay(TimeSpan.FromSeconds(10)));
        Assert.Same(received.Task, completed);
        var envelope = await received.Task;
        Assert.Equal(sent.NodeId, envelope.NodeId);
        Assert.Equal("torrentAdded", envelope.Event.GetProperty("kind").GetString());
    }

    [Fact]
    public async Task Connect_WithNoToken_Fails()
    {
        await using var connection = BuildConnection(accessToken: null);

        await Assert.ThrowsAnyAsync<Exception>(() => connection.StartAsync());
    }
}
