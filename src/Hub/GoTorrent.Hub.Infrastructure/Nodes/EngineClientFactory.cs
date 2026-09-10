using System.Net.Http.Headers;
using GoTorrent.Hub.Core.Engine;
using GoTorrent.Hub.Core.Nodes;
using GoTorrent.Hub.Infrastructure.Engine;

namespace GoTorrent.Hub.Infrastructure.Nodes;

/// <summary>
/// Builds an <see cref="EngineClient"/> per <see cref="EngineNode"/> from
/// <see cref="IHttpClientFactory"/>'s <see cref="ClientName"/>-registered
/// client (base address/token set per call here, since — unlike the
/// single statically-configured node — they're only known once a node
/// value comes out of the database) but reuses the exact same resilience
/// pipeline and request/response handling <see cref="EngineClient"/>
/// already has, rather than a second copy of either.
/// </summary>
public sealed class EngineClientFactory(IHttpClientFactory httpClientFactory) : IEngineClientFactory
{
    public const string ClientName = "gotorrent-hub-engine-node";

    public IEngineClient CreateClient(EngineNode node)
    {
        var httpClient = httpClientFactory.CreateClient(ClientName);
        httpClient.BaseAddress = node.BaseAddress;
        httpClient.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", node.Token);
        return new EngineClient(httpClient);
    }
}
