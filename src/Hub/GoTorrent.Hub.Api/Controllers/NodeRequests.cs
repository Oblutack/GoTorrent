namespace GoTorrent.Hub.Api.Controllers;

/// <summary>What a caller sends to register a new gottrentd node.</summary>
public sealed record CreateNodeRequest(string Name, string BaseAddress, string Token, bool Enabled);

/// <summary>
/// What a caller sends to update a node. <see cref="Token"/> is optional -
/// null or empty leaves the currently stored token unchanged, since
/// <see cref="NodeResponse"/> never round-trips it back out for a caller
/// to resend.
/// </summary>
public sealed record UpdateNodeRequest(string Name, string BaseAddress, string? Token, bool Enabled);

/// <summary>
/// A node as returned to a caller — deliberately never includes
/// <c>EngineNode.Token</c>. It is a real bearer credential for that
/// node's gottrentd; an API response is not the place for it to leak back
/// out, encrypted at rest or not.
/// </summary>
public sealed record NodeResponse(Guid Id, string Name, string BaseAddress, bool Enabled, DateTimeOffset CreatedAt);
