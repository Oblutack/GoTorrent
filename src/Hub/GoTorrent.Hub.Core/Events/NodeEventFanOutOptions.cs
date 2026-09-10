namespace GoTorrent.Hub.Core.Events;

/// <summary>Bound from configuration's "NodeEventFanOut" section.</summary>
public sealed class NodeEventFanOutOptions
{
    public const string SectionName = "NodeEventFanOut";

    /// <summary>How often <see cref="NodeEventFanOutCoordinator.SyncAsync"/> re-checks the registered node set for additions/removals/enabled changes.</summary>
    public TimeSpan SyncInterval { get; init; } = TimeSpan.FromSeconds(30);

    /// <summary>How long to wait before retrying a node whose event stream just disconnected.</summary>
    public TimeSpan ReconnectDelay { get; init; } = TimeSpan.FromSeconds(5);
}
