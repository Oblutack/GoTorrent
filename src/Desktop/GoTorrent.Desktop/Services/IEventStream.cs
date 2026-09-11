using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// A live feed of gottrentd's real events (<c>GET /api/v1/events</c>) —
/// separate from <see cref="IEngineClient"/> since it's a different
/// transport (WebSocket, not request/response), the same split the Hub's
/// own <c>INodeEventStream</c>/<c>IEngineClient</c> already follow.
/// </summary>
public interface IEventStream
{
    IAsyncEnumerable<WsEvent> ConnectAsync(EngineOptions options, CancellationToken cancellationToken);
}
