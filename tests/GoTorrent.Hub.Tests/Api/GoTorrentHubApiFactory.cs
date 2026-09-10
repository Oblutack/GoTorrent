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

    public GoTorrentHubApiFactory() => _connection.Open();

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
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
