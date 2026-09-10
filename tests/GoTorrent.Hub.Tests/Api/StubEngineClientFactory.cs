using GoTorrent.Hub.Core.Engine;
using GoTorrent.Hub.Core.Nodes;

namespace GoTorrent.Hub.Tests.Api;

/// <summary>
/// Hands every node the same canned <see cref="StubEngineClient"/> -
/// enough for NodesControllerTests to prove the aggregate endpoints
/// (GET .../torrents, GET .../status) wire the real repository and
/// controller together end to end, with no real HTTP call and no
/// dependency on resilience-pipeline timing. The actual per-node
/// fan-out/failure-tolerance logic is NodeAggregationServiceTests'
/// job, against fakes that can simulate one node being unreachable -
/// nothing here needs to duplicate that.
/// </summary>
public sealed class StubEngineClientFactory : IEngineClientFactory
{
    public IEngineClient CreateClient(EngineNode node) => new StubEngineClient();
}
