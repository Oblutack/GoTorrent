using GoTorrent.Hub.Core.Nodes;
using Microsoft.Extensions.DependencyInjection;

namespace GoTorrent.Hub.Infrastructure.Nodes;

public static class DependencyInjection
{
    /// <summary>
    /// Registers multi-node aggregation (ROADMAP.md's 5.2): the
    /// <see cref="IEngineNodeRepository"/>/<see cref="EngineNodeRepository"/>
    /// EF Core store (shares <c>GoTorrentHubDbContext</c> with the RSS
    /// feature — see <c>AddRssRules</c>, either extension can run first),
    /// a named, resilience-wrapped HttpClient the factory below builds a
    /// per-node client from, and <see cref="NodeAggregationService"/>
    /// itself. Also registers ASP.NET Core Data Protection
    /// (<c>AddDataProtection</c>) if nothing else in the host already has —
    /// it's what encrypts <c>EngineNode.Token</c> at rest (see
    /// ProtectedStringConverter); idempotent to call more than once.
    /// </summary>
    public static IServiceCollection AddNodeAggregation(this IServiceCollection services)
    {
        services.AddDataProtection();

        services.AddScoped<IEngineNodeRepository, EngineNodeRepository>();
        services.AddScoped<IEngineClientFactory, EngineClientFactory>();
        services.AddScoped<NodeAggregationService>();

        services.AddHttpClient(EngineClientFactory.ClientName)
            .AddStandardResilienceHandler();

        return services;
    }
}
