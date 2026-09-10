using System.Net.Http.Headers;
using GoTorrent.Hub.Core.Engine;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

namespace GoTorrent.Hub.Infrastructure.Engine;

public static class DependencyInjection
{
    /// <summary>
    /// Registers <see cref="IEngineClient"/> as a typed HttpClient talking
    /// to one gottrentd node: base address and bearer token bound from
    /// <see cref="EngineOptions.SectionName"/> in <paramref name="configuration"/>,
    /// plus a standard resilience pipeline (retry with jittered backoff,
    /// then a circuit breaker, then an overall timeout) so a node that is
    /// temporarily down or slow degrades gracefully instead of taking the
    /// Hub down with it.
    /// </summary>
    public static IServiceCollection AddEngineClient(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<EngineOptions>()
            .Bind(configuration.GetSection(EngineOptions.SectionName))
            .ValidateOnStart();

        services.AddHttpClient<IEngineClient, EngineClient>((provider, client) =>
            {
                var options = provider.GetRequiredService<IOptions<EngineOptions>>().Value;
                client.BaseAddress = options.BaseAddress;
                client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", options.Token);
            })
            .AddStandardResilienceHandler();

        return services;
    }
}
