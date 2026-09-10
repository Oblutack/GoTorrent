using GoTorrent.Hub.Core.Engine;

namespace GoTorrent.Hub.Core.Nodes;

/// <summary>
/// One node's reachability snapshot, as seen by the most recent
/// <see cref="NodeAggregationService.GetNodeStatusesAsync"/> pass — a
/// disabled node is never contacted at all, an enabled one is either
/// <see cref="Session"/>-bearing (reachable) or <see cref="Error"/>-bearing
/// (it wasn't, this time), never both.
/// </summary>
public sealed record NodeStatus(Guid NodeId, string NodeName, bool Enabled, bool Reachable, SessionStats? Session, string? Error)
{
    public static NodeStatus Disabled(EngineNode node) =>
        new(node.Id, node.Name, Enabled: false, Reachable: false, Session: null, Error: null);

    public static NodeStatus Online(EngineNode node, SessionStats session) =>
        new(node.Id, node.Name, Enabled: true, Reachable: true, Session: session, Error: null);

    public static NodeStatus Offline(EngineNode node, string error) =>
        new(node.Id, node.Name, Enabled: true, Reachable: false, Session: null, Error: error);
}
