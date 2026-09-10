using System.Net.WebSockets;
using System.Runtime.CompilerServices;
using System.Text;
using GoTorrent.Hub.Core.Events;
using GoTorrent.Hub.Core.Nodes;

namespace GoTorrent.Hub.Infrastructure.Events;

/// <summary>
/// <see cref="INodeEventStream"/> over a real <see cref="ClientWebSocket"/>
/// connected to gottrentd's own <c>GET /api/v1/events</c> (Phase 4.2's
/// hand-rolled RFC 6455 server, <c>internal/ws</c>) — this side needs no
/// hand-rolled protocol handling of its own, since <see cref="ClientWebSocket"/>
/// is a standard-compliant client and the Go server is a standard-compliant
/// (if hand-written) one. The bearer token goes on the handshake's
/// Authorization header, same as every other request this Hub makes to a
/// node — unlike a browser's native WebSocket, <see cref="ClientWebSocket"/>
/// can set arbitrary request headers, so there's no need for gottrentd's
/// own <c>?token=</c> query-parameter fallback here.
/// </summary>
public sealed class WebSocketNodeEventStream : INodeEventStream
{
    private const int ReceiveBufferSize = 16 * 1024;

    public async IAsyncEnumerable<string> ReadEventsAsync(EngineNode node, [EnumeratorCancellation] CancellationToken cancellationToken)
    {
        using var socket = new ClientWebSocket();
        socket.Options.SetRequestHeader("Authorization", $"Bearer {node.Token}");

        await socket.ConnectAsync(ToEventsUri(node.BaseAddress), cancellationToken);

        var buffer = new byte[ReceiveBufferSize];
        while (socket.State == WebSocketState.Open)
        {
            using var messageBuffer = new MemoryStream();
            WebSocketReceiveResult result;
            do
            {
                result = await socket.ReceiveAsync(buffer, cancellationToken);
                if (result.MessageType == WebSocketMessageType.Close)
                {
                    yield break;
                }
                messageBuffer.Write(buffer, 0, result.Count);
            } while (!result.EndOfMessage);

            yield return Encoding.UTF8.GetString(messageBuffer.ToArray());
        }
    }

    /// <summary>gottrentd's http(s) base address, rewritten to ws(s):// plus the events route.</summary>
    private static Uri ToEventsUri(Uri baseAddress)
    {
        var builder = new UriBuilder(baseAddress)
        {
            Scheme = baseAddress.Scheme == Uri.UriSchemeHttps ? "wss" : "ws",
            Path = baseAddress.AbsolutePath.TrimEnd('/') + "/api/v1/events",
        };
        return builder.Uri;
    }
}
