using System.Net.WebSockets;
using System.Text;
using System.Threading.Channels;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;

namespace GoTorrent.Hub.Tests.Infrastructure;

/// <summary>
/// A real, minimal WebSocket server for WebSocketNodeEventStreamTests -
/// this project's own "real fixtures over mocks" convention (see
/// SqliteDbContextFixture's own reasoning) applied here: a fake
/// <c>INodeEventStream</c> would prove nothing about whether
/// <c>WebSocketNodeEventStream</c>'s real <c>ClientWebSocket</c> usage
/// (headers, frame reassembly, close handling) actually works. Serves
/// exactly one path, <c>/api/v1/events</c>, and only ever sends whatever
/// <see cref="Send"/> queues, mirroring gottrentd's own WS event stream
/// shape closely enough for this test's purposes without needing the Go
/// binary itself.
/// </summary>
public sealed class TestWebSocketServer : IAsyncDisposable
{
    private readonly WebApplication _app;
    private readonly Channel<string> _outgoing = Channel.CreateUnbounded<string>();

    public Uri BaseAddress { get; }

    public string? ObservedAuthorizationHeader { get; private set; }

    public TestWebSocketServer()
    {
        var builder = WebApplication.CreateBuilder();
        builder.WebHost.UseUrls("http://127.0.0.1:0");
        builder.Logging.ClearProviders();
        _app = builder.Build();
        _app.UseWebSockets();
        _app.Run(async context =>
        {
            ObservedAuthorizationHeader = context.Request.Headers.Authorization.ToString();
            if (context.Request.Path != "/api/v1/events" || !context.WebSockets.IsWebSocketRequest)
            {
                context.Response.StatusCode = 400;
                return;
            }

            using var socket = await context.WebSockets.AcceptWebSocketAsync();
            await foreach (var message in _outgoing.Reader.ReadAllAsync(context.RequestAborted))
            {
                var bytes = Encoding.UTF8.GetBytes(message);
                await socket.SendAsync(bytes, WebSocketMessageType.Text, endOfMessage: true, context.RequestAborted);
            }
            await socket.CloseAsync(WebSocketCloseStatus.NormalClosure, null, context.RequestAborted);
        });

        _app.Start();
        BaseAddress = new Uri(_app.Urls.First());
    }

    /// <summary>Queues one text frame to send to whatever client is (or later becomes) connected.</summary>
    public void Send(string message) => _outgoing.Writer.TryWrite(message);

    /// <summary>Stops sending and closes the connection - lets a client's <c>await foreach</c> end naturally instead of only ending via cancellation.</summary>
    public void Complete() => _outgoing.Writer.TryComplete();

    public async ValueTask DisposeAsync() => await _app.DisposeAsync();
}
