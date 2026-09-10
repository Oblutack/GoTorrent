using GoTorrent.Hub.Core.Engine;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.DependencyInjection;

namespace GoTorrent.Hub.Tests.Api;

/// <summary>
/// Boots the real GoTorrent.Hub.Api host (real DI container, real routing,
/// real middleware pipeline) with <see cref="StubEngineClient"/> standing
/// in for a real gottrentd - proving the Api project itself is wired
/// correctly, independent of EngineClientTests' own coverage of the real
/// HTTP/JSON behavior against gottrentd's actual API shape.
/// </summary>
public sealed class GoTorrentHubApiFactory : WebApplicationFactory<Program>
{
    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.ConfigureServices(services =>
        {
            // Registered after the app's own AddEngineClient call already
            // ran (Program.cs builds its container before this factory
            // gets a chance to touch it), so this is the one DI resolves.
            services.AddScoped<IEngineClient, StubEngineClient>();
        });
    }
}
