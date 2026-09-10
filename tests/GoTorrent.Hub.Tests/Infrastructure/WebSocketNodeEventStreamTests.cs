using GoTorrent.Hub.Core.Nodes;
using GoTorrent.Hub.Infrastructure.Events;

namespace GoTorrent.Hub.Tests.Infrastructure;

public sealed class WebSocketNodeEventStreamTests
{
    private static EngineNode MakeNode(Uri baseAddress) => new()
    {
        Name = "test-node",
        BaseAddress = baseAddress,
        Token = "the-test-token",
        Enabled = true,
    };

    [Fact]
    public async Task ReadEventsAsync_ReceivesFramesSentByTheServerInOrder()
    {
        await using var server = new TestWebSocketServer();
        server.Send("frame-1");
        server.Send("frame-2");
        server.Complete();
        var stream = new WebSocketNodeEventStream();
        var node = MakeNode(server.BaseAddress);

        var received = new List<string>();
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(10));
        await foreach (var frame in stream.ReadEventsAsync(node, cts.Token))
        {
            received.Add(frame);
        }

        Assert.Equal(["frame-1", "frame-2"], received);
    }

    [Fact]
    public async Task ReadEventsAsync_SendsTheNodesTokenAsABearerAuthorizationHeader()
    {
        await using var server = new TestWebSocketServer();
        server.Complete();
        var stream = new WebSocketNodeEventStream();
        var node = MakeNode(server.BaseAddress);

        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(10));
        await foreach (var _ in stream.ReadEventsAsync(node, cts.Token))
        {
            // Draining is enough to force the connection to actually happen.
        }

        Assert.Equal("Bearer the-test-token", server.ObservedAuthorizationHeader);
    }

    [Fact]
    public async Task ReadEventsAsync_StopsPromptlyWhenCancelled()
    {
        await using var server = new TestWebSocketServer();
        // Deliberately never calls Complete() - the connection would
        // otherwise stay open forever waiting for more frames.
        var stream = new WebSocketNodeEventStream();
        var node = MakeNode(server.BaseAddress);
        using var cts = new CancellationTokenSource();

        var readTask = Task.Run(async () =>
        {
            await foreach (var _ in stream.ReadEventsAsync(node, cts.Token))
            {
            }
        }, CancellationToken.None);

        // Give the connection a moment to actually establish before
        // cancelling, so this proves cancellation-while-connected, not
        // just cancellation-before-connecting.
        await Task.Delay(TimeSpan.FromMilliseconds(200));
        await cts.CancelAsync();

        var completed = await Task.WhenAny(readTask, Task.Delay(TimeSpan.FromSeconds(10)));
        Assert.Same(readTask, completed);
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => readTask);
    }
}
