using System.Net.Http.Headers;
using System.Net.Http.Json;
using GoTorrent.Hub.Api.Controllers;
using GoTorrent.Hub.Core.Engine;
using GoTorrent.Hub.Core.Nodes;
using GoTorrent.Hub.Infrastructure.Persistence;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Data.Sqlite;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;

namespace GoTorrent.Hub.Tests.Api;

/// <summary>
/// Boots the real GoTorrent.Hub.Api host (real DI container, real routing,
/// real middleware pipeline, real EF Core migrations run at startup)
/// with <see cref="StubEngineClient"/> standing in for a real gottrentd
/// and an isolated in-memory SQLite database standing in for the Hub's
/// own persistent one - proving the Api project itself is wired
/// correctly, independent of EngineClientTests'/RssRuleRepositoryTests'
/// own coverage of the real HTTP and real-SQLite-with-constraints
/// behavior in isolation.
/// </summary>
public sealed class GoTorrentHubApiFactory : WebApplicationFactory<Program>
{
    // Kept open for the factory's lifetime - a fresh connection to
    // ":memory:" is otherwise its own separate empty database, the same
    // reasoning SqliteDbContextFixture documents for the non-Api tests.
    private readonly SqliteConnection _connection = new("Data Source=:memory:");

    // A fixed bootstrap account every test class using this factory can
    // authenticate as - CreateAuthenticatedClientAsync registers it on
    // first use (each factory instance is a fresh, isolated database) and
    // just logs in on every call after that.
    public const string TestUserName = "smoke-test-user";
    public const string TestPassword = "P@ssw0rd1234!";

    public GoTorrentHubApiFactory() => _connection.Open();

    /// <summary>
    /// A client carrying a real bearer token from the real
    /// /api/v1/auth/register + /api/v1/auth/login round trip - every
    /// controller test needs this now that every route but AuthController's
    /// own requires authorization. Proves the full auth flow works, not
    /// just that a hand-minted token would satisfy the JWT bearer handler.
    /// </summary>
    public async Task<HttpClient> CreateAuthenticatedClientAsync()
    {
        var client = CreateClient();

        var login = await client.PostAsJsonAsync("/api/v1/auth/login", new LoginRequest(TestUserName, TestPassword));
        if (login.StatusCode != System.Net.HttpStatusCode.OK)
        {
            var register = await client.PostAsJsonAsync("/api/v1/auth/register", new RegisterRequest(TestUserName, TestPassword));
            register.EnsureSuccessStatusCode();
            login = await client.PostAsJsonAsync("/api/v1/auth/login", new LoginRequest(TestUserName, TestPassword));
        }
        login.EnsureSuccessStatusCode();

        var body = await login.Content.ReadFromJsonAsync<LoginResponse>();
        client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", body!.AccessToken);
        return client;
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        // Program.cs validates Jwt:SigningKey at startup (ValidateOnStart)
        // - appsettings.json's own placeholder is deliberately empty (a
        // real secret must never be committed), so the test host needs
        // its own, same as a real deployment would via user-secrets.
        builder.UseSetting("Jwt:SigningKey", "test-only-signing-key-at-least-32-bytes-long!!");

        builder.ConfigureServices(services =>
        {
            // Registered after the app's own AddEngineClient/AddRssRules
            // calls already ran (Program.cs builds its container before
            // this factory gets a chance to touch it), so these are what
            // DI actually resolves.
            services.AddScoped<IEngineClient, StubEngineClient>();
            services.AddScoped<IEngineClientFactory, StubEngineClientFactory>();

            // Real Data Protection, but ephemeral (in-memory keys, never
            // touches this machine's real key ring) - the default
            // file-system-backed provider AddNodeAggregation registers
            // would otherwise write key files under the test process's
            // profile, which is unnecessary state for a test run and can
            // fail outright on a locked-down CI runner.
            services.RemoveAll<IDataProtectionProvider>();
            services.AddSingleton<IDataProtectionProvider>(new EphemeralDataProtectionProvider());

            services.RemoveAll<DbContextOptions<GoTorrentHubDbContext>>();
            services.AddDbContext<GoTorrentHubDbContext>(options => options.UseSqlite(_connection));
        });
    }

    protected override void Dispose(bool disposing)
    {
        base.Dispose(disposing);
        if (disposing)
        {
            _connection.Dispose();
        }
    }
}
