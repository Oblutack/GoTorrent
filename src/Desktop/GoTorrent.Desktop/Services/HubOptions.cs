namespace GoTorrent.Desktop.Services;

/// <summary>
/// Where to find a <c>GoTorrent.Hub</c> instance, plus the bearer token
/// to use once logged in. Mirrors <see cref="EngineOptions"/>'s exact
/// shape - the one difference is that <see cref="Token"/> may legitimately
/// be empty here: <see cref="IHubClient.LoginAsync"/> is called against a
/// client constructed with no token yet, since the Hub's own
/// <c>/api/v1/auth/login</c> route is unauthenticated.
/// </summary>
public sealed record HubOptions(Uri BaseAddress, string Token);
