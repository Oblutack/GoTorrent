namespace GoTorrent.Desktop.Services;

/// <summary>Where to find gottrentd and the bearer token it requires.</summary>
public sealed record EngineOptions(Uri BaseAddress, string Token);
