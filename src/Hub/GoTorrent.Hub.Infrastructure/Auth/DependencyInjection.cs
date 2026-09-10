using System.Text;
using GoTorrent.Hub.Core.Auth;
using GoTorrent.Hub.Infrastructure.Persistence;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;

namespace GoTorrent.Hub.Infrastructure.Auth;

public static class DependencyInjection
{
    /// <summary>
    /// Registers Identity + JWT (ROADMAP.md's 5.2): <c>AddIdentityCore</c>
    /// (not the full <c>AddIdentity</c> — this is a pure API with no
    /// cookie/UI sign-in, and no roles; see <c>GoTorrentHubDbContext</c>'s
    /// own doc comment) backed by <c>GoTorrentHubDbContext</c>'s EF Core
    /// store, plus <see cref="JwtTokenService"/>. <see cref="JwtOptions"/>
    /// is validated at startup — an unset or short
    /// <see cref="JwtOptions.SigningKey"/> doesn't just fail the first
    /// login, it would make every token this Hub ever issues forgeable,
    /// so this fails fast instead.
    /// </summary>
    public static IServiceCollection AddIdentityAndJwt(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<JwtOptions>()
            .Bind(configuration.GetSection(JwtOptions.SectionName))
            .Validate(
                o => !string.IsNullOrWhiteSpace(o.SigningKey) && Encoding.UTF8.GetByteCount(o.SigningKey) >= 32,
                "Jwt:SigningKey must be set (via dotnet user-secrets, never appsettings.json) to at least 32 bytes (256 bits).")
            .ValidateOnStart();

        services.AddIdentityCore<IdentityUser<Guid>>(identity =>
            {
                // A remote-reachable login is worth a slightly stronger
                // minimum than Identity's own default (6); lockout stays
                // at Identity's sensible defaults (5 failed attempts,
                // 5-minute lockout) - see AuthController for why that
                // needs manual UserManager calls to actually take effect.
                identity.Password.RequiredLength = 8;
            })
            .AddEntityFrameworkStores<GoTorrentHubDbContext>();

        services.AddScoped<JwtTokenService>();
        services.TryAddSingleton(TimeProvider.System);

        return services;
    }
}
