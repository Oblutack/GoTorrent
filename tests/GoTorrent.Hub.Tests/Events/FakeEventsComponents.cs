using System.Collections.Concurrent;
using System.Runtime.CompilerServices;
using System.Threading.Channels;
using GoTorrent.Hub.Core.Events;
using GoTorrent.Hub.Core.Nodes;

namespace GoTorrent.Hub.Tests.Events;

/// <summary>
/// A controllable <see cref="INodeEventStream"/> for
/// NodeEventFanOutCoordinatorTests: each node gets its own unbounded
/// channel the test can push raw JSON frames into on demand, plus
/// connect/cancel signals so a test can deterministically wait for "the
/// coordinator has actually started reading this node" or "actually
/// cancelled this node's subscription" instead of sleeping and hoping.
/// </summary>
public sealed class FakeNodeEventStream : INodeEventStream
{
    private readonly ConcurrentDictionary<Guid, Channel<string>> _channels = new();
    private readonly ConcurrentDictionary<Guid, Exception> _failures = new();
    private readonly ConcurrentDictionary<Guid, TaskCompletionSource> _connectSignals = new();
    private readonly ConcurrentDictionary<Guid, TaskCompletionSource> _cancelSignals = new();
    private int _connectCount;

    public int ConnectCount => _connectCount;

    public Channel<string> ChannelFor(EngineNode node) =>
        _channels.GetOrAdd(node.Id, _ => Channel.CreateUnbounded<string>());

    public void SetFailure(EngineNode node, Exception exception) => _failures[node.Id] = exception;

    public async Task WaitForConnectAsync(EngineNode node, TimeSpan timeout)
    {
        var signal = _connectSignals.GetOrAdd(node.Id, _ => new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously));
        using var cts = new CancellationTokenSource(timeout);
        await using var registration = cts.Token.Register(() => signal.TrySetCanceled());
        await signal.Task;
    }

    public async Task WaitForCancelAsync(EngineNode node, TimeSpan timeout)
    {
        var signal = _cancelSignals.GetOrAdd(node.Id, _ => new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously));
        using var cts = new CancellationTokenSource(timeout);
        await using var registration = cts.Token.Register(() => signal.TrySetCanceled());
        await signal.Task;
    }

    public async IAsyncEnumerable<string> ReadEventsAsync(EngineNode node, [EnumeratorCancellation] CancellationToken cancellationToken)
    {
        Interlocked.Increment(ref _connectCount);
        _connectSignals.GetOrAdd(node.Id, _ => new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously)).TrySetResult();
        await using var registration = cancellationToken.Register(() =>
            _cancelSignals.GetOrAdd(node.Id, _ => new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously)).TrySetResult());

        if (_failures.TryGetValue(node.Id, out var exception))
        {
            throw exception;
        }

        var channel = ChannelFor(node);
        await foreach (var item in channel.Reader.ReadAllAsync(cancellationToken))
        {
            yield return item;
        }
    }
}

/// <summary>A recording <see cref="INodeEventBroadcaster"/> - tests consume broadcasts via <see cref="ReceiveAsync"/> rather than polling a list.</summary>
public sealed class FakeNodeEventBroadcaster : INodeEventBroadcaster
{
    private readonly Channel<NodeEventEnvelope> _channel = Channel.CreateUnbounded<NodeEventEnvelope>();

    public Task BroadcastAsync(NodeEventEnvelope envelope, CancellationToken cancellationToken)
    {
        _channel.Writer.TryWrite(envelope);
        return Task.CompletedTask;
    }

    /// <summary>Waits for the next broadcast, bounded by <paramref name="timeout"/> so a bug that stops broadcasting fails the test instead of hanging it.</summary>
    public async Task<NodeEventEnvelope> ReceiveAsync(TimeSpan timeout)
    {
        using var cts = new CancellationTokenSource(timeout);
        return await _channel.Reader.ReadAsync(cts.Token);
    }

    /// <summary>Asserts no broadcast arrives within <paramref name="window"/> - the only honest way to prove a negative here, kept short.</summary>
    public async Task<bool> NoBroadcastArrivesWithinAsync(TimeSpan window)
    {
        using var cts = new CancellationTokenSource(window);
        try
        {
            await _channel.Reader.ReadAsync(cts.Token);
            return false;
        }
        catch (OperationCanceledException)
        {
            return true;
        }
    }
}
