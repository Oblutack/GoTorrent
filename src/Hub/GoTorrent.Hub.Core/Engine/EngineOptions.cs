namespace GoTorrent.Hub.Core.Engine;

/// <summary>
/// Configuration for one gottrentd node this Hub talks to. Bound from
/// configuration (the "Engine" section of appsettings.json, or an
/// environment/secret override) via
/// <see cref="Microsoft.Extensions.Options.IOptions{TOptions}"/>.
/// </summary>
/// <remarks>
/// One node today — 5.2's multi-node aggregation will need a named
/// collection of these (one gottrentd per registered node) rather than
/// this single-section shape, but that is a real feature to design when
/// it is actually built, not something to guess at here.
/// </remarks>
public sealed class EngineOptions
{
    public const string SectionName = "Engine";

    /// <summary>gottrentd's control-API base address, e.g. "http://127.0.0.1:6880/".</summary>
    public required Uri BaseAddress { get; init; }

    /// <summary>
    /// The bearer token gottrentd generated on first run (its own
    /// <c>api-token</c> file, next to its config) — required on every
    /// request past gottrentd's <c>RequireBearerToken</c> middleware.
    /// </summary>
    public required string Token { get; init; }
}
