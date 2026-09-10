namespace GoTorrent.Hub.Core.Nodes;

/// <summary>
/// One registered gottrentd instance (ROADMAP.md's 5.2 multi-node
/// aggregation — "register several gottrentd instances ... aggregate
/// their state behind one API"). Persisted via
/// <see cref="IEngineNodeRepository"/>; turned into a real
/// <see cref="Engine.IEngineClient"/> on demand by
/// <see cref="IEngineClientFactory"/> — nothing here talks HTTP directly.
/// </summary>
/// <remarks>
/// <see cref="Token"/> is always plaintext at this layer. Encrypting it at
/// rest is a persistence concern (an EF Core value converter over
/// Data Protection — see GoTorrentHubDbContext), not something this domain
/// type or its callers should have to know about.
/// </remarks>
public sealed class EngineNode
{
    public Guid Id { get; init; } = Guid.NewGuid();

    public required string Name { get; set; }

    /// <summary>gottrentd's control-API base address, e.g. "http://127.0.0.1:6880/".</summary>
    public required Uri BaseAddress { get; set; }

    /// <summary>The bearer token this node's gottrentd requires (its own <c>api-token</c> file).</summary>
    public required string Token { get; set; }

    /// <summary>
    /// A disabled node is skipped by <see cref="NodeAggregationService"/>
    /// entirely — registered but temporarily excluded, without deleting
    /// its record (and re-typing its token) to take it out of rotation.
    /// </summary>
    public bool Enabled { get; set; } = true;

    public DateTimeOffset CreatedAt { get; init; } = DateTimeOffset.UtcNow;
}
