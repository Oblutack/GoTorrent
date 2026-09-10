namespace GoTorrent.Hub.Core.Events;

/// <summary>
/// Fans one <see cref="NodeEventEnvelope"/> out to every connected
/// client. Implemented against SignalR's <c>IHubContext</c> in the Api
/// project (not Infrastructure — a SignalR hub is a hosting-pipeline
/// concept, the same reasoning <c>EngineHealthCheck</c> already
/// established for living in Api despite depending on a Core
/// abstraction).
/// </summary>
public interface INodeEventBroadcaster
{
    Task BroadcastAsync(NodeEventEnvelope envelope, CancellationToken cancellationToken);
}
