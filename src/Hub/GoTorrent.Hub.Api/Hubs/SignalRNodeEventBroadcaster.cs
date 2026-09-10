using GoTorrent.Hub.Core.Events;
using Microsoft.AspNetCore.SignalR;

namespace GoTorrent.Hub.Api.Hubs;

/// <summary>
/// <see cref="INodeEventBroadcaster"/> over <see cref="IHubContext{T}"/> —
/// lives here rather than Infrastructure because a SignalR hub is a
/// hosting-pipeline concept (needs <c>AddSignalR()</c>/<c>MapHub</c> in
/// this same Api project), the same reasoning <c>EngineHealthCheck</c>
/// already established for living in Api despite depending on a Core
/// abstraction (<c>IEngineClient</c>) to do its job.
/// </summary>
public sealed class SignalRNodeEventBroadcaster(IHubContext<GoTorrentEventsHub> hub) : INodeEventBroadcaster
{
    public Task BroadcastAsync(NodeEventEnvelope envelope, CancellationToken cancellationToken) =>
        hub.Clients.All.SendAsync("NodeEvent", envelope, cancellationToken);
}
