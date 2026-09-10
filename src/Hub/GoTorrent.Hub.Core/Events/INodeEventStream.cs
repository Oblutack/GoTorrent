using GoTorrent.Hub.Core.Nodes;

namespace GoTorrent.Hub.Core.Events;

/// <summary>
/// Opens one node's own live WebSocket event stream and yields each
/// message as raw JSON text, one string per frame. Implemented against a
/// real <c>ClientWebSocket</c> in Infrastructure; <see cref="NodeEventFanOutCoordinator"/>
/// is the only caller, and treats a thrown exception (connection
/// refused, dropped mid-stream, anything) as "this node's stream ended,
/// try again later" rather than something to propagate.
/// </summary>
public interface INodeEventStream
{
    IAsyncEnumerable<string> ReadEventsAsync(EngineNode node, CancellationToken cancellationToken);
}
