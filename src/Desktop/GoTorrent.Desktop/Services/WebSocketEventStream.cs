using System.Net.WebSockets;
using System.Runtime.CompilerServices;
using System.Text;
using System.Text.Json;
using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// <see cref="IEventStream"/> over a real <see cref="ClientWebSocket"/>
/// connected to gottrentd's own <c>GET /api/v1/events</c> (Phase 4.2's
/// hand-rolled RFC 6455 server, <c>internal/ws</c>) — same shape as the
/// Hub's own <c>WebSocketNodeEventStream</c>, since it's the identical
/// server on the other end. The bearer token goes on the handshake's
/// Authorization header; a real WebSocket client (unlike a browser's) can
/// set arbitrary request headers, so gottrentd's <c>?token=</c>
/// query-parameter fallback isn't needed here.
/// </summary>
public sealed class WebSocketEventStream : IEventStream
{
    private const int ReceiveBufferSize = 16 * 1024;
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    public async IAsyncEnumerable<WsEvent> ConnectAsync(EngineOptions options, [EnumeratorCancellation] CancellationToken cancellationToken)
    {
        using var socket = new ClientWebSocket();
        socket.Options.SetRequestHeader("Authorization", $"Bearer {options.Token}");
        await socket.ConnectAsync(ToEventsUri(options.BaseAddress), cancellationToken);

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

            var json = Encoding.UTF8.GetString(messageBuffer.ToArray());
            WsEvent? ev = null;
            try
            {
                ev = JsonSerializer.Deserialize<WsEvent>(json, JsonOptions);
            }
            catch (JsonException)
            {
                // A malformed message from gottrentd is worth ignoring, not
                // worth tearing down an otherwise-working connection over.
            }
            if (ev is not null)
            {
                yield return ev;
            }
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
