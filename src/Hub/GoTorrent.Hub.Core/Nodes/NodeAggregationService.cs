using Microsoft.Extensions.Logging;

namespace GoTorrent.Hub.Core.Nodes;

/// <summary>
/// The actual "aggregate several gottrentd instances behind one API"
/// logic (ROADMAP.md's 5.2) — everything else in this feature (the
/// repository, the client factory, NodesController) exists to feed this.
/// Fans out to every node concurrently rather than one at a time: a
/// sequential pass would make the whole aggregate call as slow as the sum
/// of every dead/slow node's resilience-pipeline timeout, instead of just
/// the single slowest one. One unreachable node is logged and excluded
/// from the result rather than failing the whole aggregate — the point of
/// aggregating several nodes is that one being down shouldn't take the
/// others down with it.
/// </summary>
public sealed class NodeAggregationService(
    IEngineNodeRepository nodes,
    IEngineClientFactory clientFactory,
    ILogger<NodeAggregationService> logger)
{
    public async Task<IReadOnlyList<AggregatedTorrentSummary>> GetAggregatedTorrentsAsync(CancellationToken cancellationToken)
    {
        var enabledNodes = (await nodes.GetAllAsync(cancellationToken)).Where(n => n.Enabled).ToList();
        var perNode = await Task.WhenAll(enabledNodes.Select(n => GetNodeTorrentsAsync(n, cancellationToken)));
        return [.. perNode.SelectMany(t => t)];
    }

    public async Task<IReadOnlyList<NodeStatus>> GetNodeStatusesAsync(CancellationToken cancellationToken)
    {
        var allNodes = await nodes.GetAllAsync(cancellationToken);
        return await Task.WhenAll(allNodes.Select(n => GetNodeStatusAsync(n, cancellationToken)));
    }

    private async Task<IReadOnlyList<AggregatedTorrentSummary>> GetNodeTorrentsAsync(EngineNode node, CancellationToken cancellationToken)
    {
        try
        {
            var client = clientFactory.CreateClient(node);
            var torrents = await client.ListTorrentsAsync(cancellationToken);
            return [.. torrents.Select(t => new AggregatedTorrentSummary(node.Id, node.Name, t))];
        }
        catch (Exception ex) when (ex is not OperationCanceledException)
        {
            logger.LogWarning(ex, "Node {NodeName} ({NodeId}) unreachable, excluded from the aggregated torrent list", node.Name, node.Id);
            return [];
        }
    }

    private async Task<NodeStatus> GetNodeStatusAsync(EngineNode node, CancellationToken cancellationToken)
    {
        if (!node.Enabled)
        {
            return NodeStatus.Disabled(node);
        }
        try
        {
            var client = clientFactory.CreateClient(node);
            var stats = await client.GetSessionAsync(cancellationToken);
            return NodeStatus.Online(node, stats);
        }
        catch (Exception ex) when (ex is not OperationCanceledException)
        {
            logger.LogWarning(ex, "Node {NodeName} ({NodeId}) unreachable", node.Name, node.Id);
            return NodeStatus.Offline(node, ex.Message);
        }
    }
}
