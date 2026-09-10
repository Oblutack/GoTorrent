using System.Text.Json;
using GoTorrent.Hub.Core.Nodes;
using Microsoft.Extensions.Logging;

namespace GoTorrent.Hub.Core.Events;

/// <summary>
/// Keeps exactly one live subscription running per enabled registered
/// node, relaying every event that node's own WebSocket stream produces
/// into <see cref="INodeEventBroadcaster"/> — the actual "one Hub
/// connection feeding the desktop app from the same stream" ROADMAP.md's
/// 5.2 SignalR fan-out calls for. Unlike <c>RssPollCycleRunner</c>/
/// <c>NodeAggregationService</c>/<c>HistoryRecorder</c> (stateless, one
/// self-contained pass per call), this coordinator owns long-running
/// background work across calls: <see cref="Sync"/> is meant to be
/// called repeatedly (on a timer, from <c>NodeEventFanOutService</c>) and
/// only starts/stops subscriptions for what actually changed since the
/// last call, so it's safe — and cheap — to call it often.
/// </summary>
/// <remarks>
/// A node's <c>BaseAddress</c>/<c>Token</c> changing while already
/// subscribed is not picked up until that node is removed and re-added
/// (or the process restarts) — a deliberate, documented v1 simplification,
/// not an oversight; reacting to an in-place edit would need diffing more
/// than just which node IDs are present.
/// </remarks>
public sealed class NodeEventFanOutCoordinator(
    INodeEventStream eventStream,
    INodeEventBroadcaster broadcaster,
    NodeEventFanOutOptions options,
    ILogger<NodeEventFanOutCoordinator> logger)
{
    private readonly Dictionary<Guid, CancellationTokenSource> _active = [];
    private readonly Lock _lock = new();

    public void Sync(IReadOnlyList<EngineNode> enabledNodes, CancellationToken cancellationToken)
    {
        var enabledIds = enabledNodes.Select(n => n.Id).ToHashSet();

        lock (_lock)
        {
            foreach (var staleId in _active.Keys.Where(id => !enabledIds.Contains(id)).ToList())
            {
                _active[staleId].Cancel();
                _active[staleId].Dispose();
                _active.Remove(staleId);
            }

            foreach (var node in enabledNodes)
            {
                if (_active.ContainsKey(node.Id))
                {
                    continue;
                }
                var subscriptionCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
                _active[node.Id] = subscriptionCts;
                _ = RunSubscriptionAsync(node, subscriptionCts.Token);
            }
        }
    }

    /// <summary>Cancels every active subscription. Called once, on shutdown.</summary>
    public void StopAll()
    {
        lock (_lock)
        {
            foreach (var cts in _active.Values)
            {
                cts.Cancel();
                cts.Dispose();
            }
            _active.Clear();
        }
    }

    private async Task RunSubscriptionAsync(EngineNode node, CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            try
            {
                await foreach (var rawEvent in eventStream.ReadEventsAsync(node, cancellationToken))
                {
                    NodeEventEnvelope envelope;
                    try
                    {
                        envelope = new NodeEventEnvelope(node.Id, node.Name, JsonDocument.Parse(rawEvent).RootElement.Clone());
                    }
                    catch (JsonException ex)
                    {
                        // A malformed frame from one node is that node's
                        // problem, not a reason to drop its whole
                        // subscription - log and move on to the next frame.
                        logger.LogWarning(ex, "Node {NodeName} ({NodeId}) sent an unparseable event frame, skipped", node.Name, node.Id);
                        continue;
                    }
                    await broadcaster.BroadcastAsync(envelope, cancellationToken);
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                return;
            }
            catch (Exception ex)
            {
                logger.LogWarning(ex, "Node {NodeName} ({NodeId}) event stream disconnected, retrying in {Delay}", node.Name, node.Id, options.ReconnectDelay);
            }

            try
            {
                await Task.Delay(options.ReconnectDelay, cancellationToken);
            }
            catch (OperationCanceledException)
            {
                return;
            }
        }
    }
}
