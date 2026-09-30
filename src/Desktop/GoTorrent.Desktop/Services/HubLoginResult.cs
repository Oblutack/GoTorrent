namespace GoTorrent.Desktop.Services;

/// <summary>Mirrors the Hub's real <c>LoginResponse</c> JSON shape.</summary>
public sealed record HubLoginResult(string AccessToken, DateTimeOffset ExpiresAt);
