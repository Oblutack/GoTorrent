using System.IdentityModel.Tokens.Jwt;
using System.Text;
using GoTorrent.Hub.Core.Auth;
using GoTorrent.Hub.Infrastructure.Auth;
using GoTorrent.Hub.Tests.History;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.Options;
using Microsoft.IdentityModel.Tokens;

namespace GoTorrent.Hub.Tests.Auth;

public sealed class JwtTokenServiceTests
{
    private static JwtOptions MakeOptions() => new()
    {
        Issuer = "GoTorrent.Hub.Tests",
        Audience = "GoTorrent.Hub.Tests.Clients",
        // 32+ bytes, same minimum AddIdentityAndJwt's .Validate() enforces for real.
        SigningKey = "this-is-a-32-byte-or-longer-test-signing-key!!",
        AccessTokenLifetime = TimeSpan.FromHours(2),
    };

    [Fact]
    public void CreateToken_ProducesATokenValidAgainstTheSameParameters()
    {
        var options = MakeOptions();
        var time = new FixedTimeProvider(new DateTimeOffset(2026, 9, 10, 12, 0, 0, TimeSpan.Zero));
        var service = new JwtTokenService(Options.Create(options), time);
        var user = new IdentityUser<Guid> { Id = Guid.NewGuid(), UserName = "alice" };

        var (accessToken, expiresAt) = service.CreateToken(user);

        Assert.Equal(time.Now + options.AccessTokenLifetime, expiresAt);

        var validationParameters = new TokenValidationParameters
        {
            ValidateIssuer = true,
            ValidIssuer = options.Issuer,
            ValidateAudience = true,
            ValidAudience = options.Audience,
            ValidateLifetime = true,
            ValidateIssuerSigningKey = true,
            IssuerSigningKey = new SymmetricSecurityKey(Encoding.UTF8.GetBytes(options.SigningKey)),
            ClockSkew = TimeSpan.Zero,
        };
        // MapInboundClaims defaults to true, which silently rewrites
        // standard claim types like "sub" to legacy WS-Fed URIs on the
        // way out of ValidateToken - Program.cs's real JwtBearerOptions
        // setup disables the same thing, for the same reason: otherwise
        // User.FindFirst(JwtRegisteredClaimNames.Sub) would return null
        // for every authenticated request.
        var handler = new JwtSecurityTokenHandler { MapInboundClaims = false };
        var principal = handler.ValidateToken(accessToken, validationParameters, out var validatedToken);

        Assert.Equal(user.Id.ToString(), principal.FindFirst(JwtRegisteredClaimNames.Sub)?.Value);
        Assert.Equal("alice", principal.FindFirst(JwtRegisteredClaimNames.UniqueName)?.Value);
        Assert.Equal(expiresAt.UtcDateTime, validatedToken.ValidTo);
    }

    [Fact]
    public void CreateToken_RejectsValidationWithAWrongSigningKey()
    {
        var options = MakeOptions();
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var service = new JwtTokenService(Options.Create(options), time);
        var user = new IdentityUser<Guid> { Id = Guid.NewGuid(), UserName = "bob" };
        var (accessToken, _) = service.CreateToken(user);

        var wrongKeyParameters = new TokenValidationParameters
        {
            ValidateIssuer = true,
            ValidIssuer = options.Issuer,
            ValidateAudience = true,
            ValidAudience = options.Audience,
            ValidateIssuerSigningKey = true,
            IssuerSigningKey = new SymmetricSecurityKey(Encoding.UTF8.GetBytes("a-completely-different-32-byte-key!!")),
        };

        Assert.Throws<SecurityTokenSignatureKeyNotFoundException>(
            () => new JwtSecurityTokenHandler().ValidateToken(accessToken, wrongKeyParameters, out _));
    }

    [Fact]
    public void CreateToken_TwoTokensForTheSameUserHaveDifferentJtiClaims()
    {
        var options = MakeOptions();
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var service = new JwtTokenService(Options.Create(options), time);
        var user = new IdentityUser<Guid> { Id = Guid.NewGuid(), UserName = "carol" };

        var (first, _) = service.CreateToken(user);
        var (second, _) = service.CreateToken(user);

        var handler = new JwtSecurityTokenHandler();
        var firstJti = handler.ReadJwtToken(first).Id;
        var secondJti = handler.ReadJwtToken(second).Id;
        Assert.NotEqual(firstJti, secondJti);
    }

    [Fact]
    public void AppsettingsAccessTokenLifetimeString_ParsesToExactlyOneDay()
    {
        // Regression guard for a real bug caught via a smoke test:
        // TimeSpan.Parse("24:00:00") is 24 DAYS, not 24 hours - .NET's
        // TimeSpan string parser treats a leading component of 24 or
        // more as days rather than overflowing hours (so "23:00:00" is
        // fine, "24:00:00" silently isn't). appsettings.json's
        // AccessTokenLifetime must stay written in the unambiguous
        // "d.hh:mm:ss" form.
        var parsed = TimeSpan.Parse("1.00:00:00");

        Assert.Equal(TimeSpan.FromHours(24), parsed);
    }
}
