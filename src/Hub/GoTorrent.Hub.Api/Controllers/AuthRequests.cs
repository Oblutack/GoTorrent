namespace GoTorrent.Hub.Api.Controllers;

public sealed record RegisterRequest(string UserName, string Password);

public sealed record LoginRequest(string UserName, string Password);

public sealed record LoginResponse(string AccessToken, DateTimeOffset ExpiresAt);
