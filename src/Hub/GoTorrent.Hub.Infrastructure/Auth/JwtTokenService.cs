using System.IdentityModel.Tokens.Jwt;
using System.Security.Claims;
using System.Text;
using GoTorrent.Hub.Core.Auth;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.Options;
using Microsoft.IdentityModel.Tokens;

namespace GoTorrent.Hub.Infrastructure.Auth;

/// <summary>
/// Issues the access token <c>AuthController.Login</c> hands back — a
/// real signed JWT (HMAC-SHA256), not a bespoke token format, so any
/// standard JWT-aware client library can consume it. Deliberately
/// access-token-only: no refresh-token flow (that needs its own
/// persisted, rotatable store and revocation story — a real feature,
/// left open rather than half-built) — a client whose token expires just
/// logs in again, which is a fine trade for a first-party desktop client
/// talking to its own Hub.
/// </summary>
public sealed class JwtTokenService(IOptions<JwtOptions> options, TimeProvider timeProvider)
{
    public (string AccessToken, DateTimeOffset ExpiresAt) CreateToken(IdentityUser<Guid> user)
    {
        var opts = options.Value;
        var now = timeProvider.GetUtcNow();
        var expiresAt = now + opts.AccessTokenLifetime;

        var claims = new[]
        {
            new Claim(JwtRegisteredClaimNames.Sub, user.Id.ToString()),
            new Claim(JwtRegisteredClaimNames.UniqueName, user.UserName ?? user.Id.ToString()),
            new Claim(JwtRegisteredClaimNames.Jti, Guid.NewGuid().ToString()),
        };
        var signingKey = new SymmetricSecurityKey(Encoding.UTF8.GetBytes(opts.SigningKey));
        var credentials = new SigningCredentials(signingKey, SecurityAlgorithms.HmacSha256);
        var token = new JwtSecurityToken(
            opts.Issuer, opts.Audience, claims, now.UtcDateTime, expiresAt.UtcDateTime, credentials);

        return (new JwtSecurityTokenHandler().WriteToken(token), expiresAt);
    }
}
