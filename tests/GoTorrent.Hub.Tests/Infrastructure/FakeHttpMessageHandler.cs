using System.Net;
using System.Text;

namespace GoTorrent.Hub.Tests.Infrastructure;

/// <summary>
/// A minimal <see cref="HttpMessageHandler"/> that returns a fixed
/// response body for every request - enough to unit-test
/// <c>EngineClient</c>'s JSON deserialization against real gottrentd
/// response shapes without a live daemon, while still exercising the real
/// <see cref="HttpClient"/> pipeline (headers, base address resolution,
/// System.Net.Http.Json) rather than mocking EngineClient's own HTTP
/// calls away entirely.
/// </summary>
public sealed class FakeHttpMessageHandler(HttpStatusCode statusCode, string jsonBody) : HttpMessageHandler
{
    public HttpRequestMessage? LastRequest { get; private set; }

    protected override Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request, CancellationToken cancellationToken)
    {
        LastRequest = request;
        var response = new HttpResponseMessage(statusCode)
        {
            Content = new StringContent(jsonBody, Encoding.UTF8, "application/json"),
        };
        return Task.FromResult(response);
    }
}
