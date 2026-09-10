namespace GoTorrent.Hub.Core.Auth;

/// <summary>
/// Bound from configuration's "Jwt" section. <see cref="SigningKey"/> is
/// a real credential — like <c>EngineOptions.Token</c>, it must go
/// through <c>dotnet user-secrets</c> in development and never sit in
/// <c>appsettings.json</c>; unlike <c>EngineOptions.Token</c>, an empty
/// or short value here doesn't just fail to authenticate, it makes every
/// token this Hub issues forgeable, so it's validated at startup (see
/// <c>AddIdentityAndJwt</c>) rather than only failing the first time a
/// token is actually created.
/// </summary>
public sealed class JwtOptions
{
    public const string SectionName = "Jwt";

    public required string Issuer { get; init; }

    public required string Audience { get; init; }

    public required string SigningKey { get; init; }

    public TimeSpan AccessTokenLifetime { get; init; } = TimeSpan.FromHours(24);
}
