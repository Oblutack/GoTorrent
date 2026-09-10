using GoTorrent.Hub.Core.Events;
using Microsoft.Extensions.DependencyInjection;

namespace GoTorrent.Hub.Infrastructure.Events;

public static class DependencyInjection
{
    /// <summary>
    /// Registers the client side of SignalR fan-out (ROADMAP.md's 5.2):
    /// <see cref="WebSocketNodeEventStream"/>, stateless and safe as a
    /// singleton. The broadcast side (<c>INodeEventBroadcaster</c>,
    /// <c>NodeEventFanOutCoordinator</c>, SignalR itself) is registered
    /// in the Api project instead — see its own reasoning for why.
    /// </summary>
    public static IServiceCollection AddNodeEventStream(this IServiceCollection services)
    {
        services.AddSingleton<INodeEventStream, WebSocketNodeEventStream>();
        return services;
    }
}
