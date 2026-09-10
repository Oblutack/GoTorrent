using GoTorrent.Hub.Core.Events;
using GoTorrent.Hub.Core.Nodes;
using Microsoft.Extensions.Logging.Abstractions;

namespace GoTorrent.Hub.Tests.Events;

public sealed class NodeEventFanOutCoordinatorTests
{
    private static readonly TimeSpan ShortWait = TimeSpan.FromSeconds(5);

    private static EngineNode MakeNode(string name) => new()
    {
        Name = name,
        BaseAddress = new Uri($"http://{name}.example/"),
        Token = "irrelevant-in-tests",
        Enabled = true,
    };

    private static NodeEventFanOutCoordinator MakeCoordinator(
        FakeNodeEventStream stream, FakeNodeEventBroadcaster broadcaster, TimeSpan? reconnectDelay = null) =>
        new(stream, broadcaster, new NodeEventFanOutOptions { ReconnectDelay = reconnectDelay ?? TimeSpan.FromSeconds(5) },
            NullLogger<NodeEventFanOutCoordinator>.Instance);

    [Fact]
    public async Task Sync_RelaysAndTagsEventsFromAnEnabledNode()
    {
        var stream = new FakeNodeEventStream();
        var broadcaster = new FakeNodeEventBroadcaster();
        var coordinator = MakeCoordinator(stream, broadcaster);
        var node = MakeNode("node-a");

        coordinator.Sync([node], CancellationToken.None);
        await stream.WaitForConnectAsync(node, ShortWait);
        await stream.ChannelFor(node).Writer.WriteAsync("""{"kind":"torrentAdded","infoHash":"abc"}""");

        var envelope = await broadcaster.ReceiveAsync(ShortWait);

        Assert.Equal(node.Id, envelope.NodeId);
        Assert.Equal("node-a", envelope.NodeName);
        Assert.Equal("torrentAdded", envelope.Event.GetProperty("kind").GetString());
    }

    [Fact]
    public async Task Sync_RemovingANodeCancelsItsSubscriptionAndStopsRelaying()
    {
        var stream = new FakeNodeEventStream();
        var broadcaster = new FakeNodeEventBroadcaster();
        var coordinator = MakeCoordinator(stream, broadcaster);
        var node = MakeNode("node-a");

        coordinator.Sync([node], CancellationToken.None);
        await stream.WaitForConnectAsync(node, ShortWait);

        coordinator.Sync([], CancellationToken.None);
        await stream.WaitForCancelAsync(node, ShortWait);

        // The channel still exists and still accepts writes - what
        // matters is nothing is listening to it anymore.
        await stream.ChannelFor(node).Writer.WriteAsync("""{"kind":"torrentAdded"}""");
        Assert.True(await broadcaster.NoBroadcastArrivesWithinAsync(TimeSpan.FromMilliseconds(300)));
    }

    [Fact]
    public async Task Sync_AddingANodeLaterStartsRelayingFromIt()
    {
        var stream = new FakeNodeEventStream();
        var broadcaster = new FakeNodeEventBroadcaster();
        var coordinator = MakeCoordinator(stream, broadcaster);
        var node = MakeNode("node-a");

        coordinator.Sync([], CancellationToken.None);
        coordinator.Sync([node], CancellationToken.None);
        await stream.WaitForConnectAsync(node, ShortWait);
        await stream.ChannelFor(node).Writer.WriteAsync("""{"kind":"torrentAdded"}""");

        var envelope = await broadcaster.ReceiveAsync(ShortWait);
        Assert.Equal(node.Id, envelope.NodeId);
    }

    [Fact]
    public async Task Sync_CalledTwiceWithTheSameNodeDoesNotOpenASecondConnection()
    {
        var stream = new FakeNodeEventStream();
        var broadcaster = new FakeNodeEventBroadcaster();
        var coordinator = MakeCoordinator(stream, broadcaster);
        var node = MakeNode("node-a");

        coordinator.Sync([node], CancellationToken.None);
        await stream.WaitForConnectAsync(node, ShortWait);
        coordinator.Sync([node], CancellationToken.None);
        coordinator.Sync([node], CancellationToken.None);

        Assert.Equal(1, stream.ConnectCount);
    }

    [Fact]
    public async Task Sync_OneNodesFailingStreamDoesNotStopAnotherNodesRelay()
    {
        var stream = new FakeNodeEventStream();
        var broadcaster = new FakeNodeEventBroadcaster();
        // A short reconnect delay so nodeA's continuous retry loop
        // doesn't slow this test down while it keeps failing.
        var coordinator = MakeCoordinator(stream, broadcaster, TimeSpan.FromMilliseconds(20));
        var brokenNode = MakeNode("broken-node");
        var healthyNode = MakeNode("healthy-node");
        stream.SetFailure(brokenNode, new InvalidOperationException("connection refused"));

        coordinator.Sync([brokenNode, healthyNode], CancellationToken.None);
        await stream.WaitForConnectAsync(healthyNode, ShortWait);
        await stream.ChannelFor(healthyNode).Writer.WriteAsync("""{"kind":"torrentAdded"}""");

        var envelope = await broadcaster.ReceiveAsync(ShortWait);
        Assert.Equal(healthyNode.Id, envelope.NodeId);
    }

    [Fact]
    public async Task Sync_AMalformedEventFrameIsSkippedNotFatalToTheSubscription()
    {
        var stream = new FakeNodeEventStream();
        var broadcaster = new FakeNodeEventBroadcaster();
        var coordinator = MakeCoordinator(stream, broadcaster);
        var node = MakeNode("node-a");

        coordinator.Sync([node], CancellationToken.None);
        await stream.WaitForConnectAsync(node, ShortWait);
        await stream.ChannelFor(node).Writer.WriteAsync("not valid json");
        await stream.ChannelFor(node).Writer.WriteAsync("""{"kind":"torrentAdded"}""");

        var envelope = await broadcaster.ReceiveAsync(ShortWait);
        Assert.Equal("torrentAdded", envelope.Event.GetProperty("kind").GetString());
    }
}
