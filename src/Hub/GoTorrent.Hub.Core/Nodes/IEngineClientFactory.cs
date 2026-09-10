using GoTorrent.Hub.Core.Engine;

namespace GoTorrent.Hub.Core.Nodes;

/// <summary>
/// Builds an <see cref="IEngineClient"/> for an arbitrary registered
/// <see cref="EngineNode"/> at call time — unlike the single configured
/// node wired up by <c>AddEngineClient</c> (one typed HttpClient, bound
/// once at startup), a node here is a runtime value from the database, so
/// its base address and token can only be known when
/// <see cref="NodeAggregationService"/> is actually fanning out to it.
/// </summary>
public interface IEngineClientFactory
{
    IEngineClient CreateClient(EngineNode node);
}
