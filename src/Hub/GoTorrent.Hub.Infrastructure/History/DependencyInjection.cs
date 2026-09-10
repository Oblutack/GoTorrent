using GoTorrent.Hub.Core.History;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

namespace GoTorrent.Hub.Infrastructure.History;

public static class DependencyInjection
{
    /// <summary>
    /// Registers history/analytics (ROADMAP.md's 5.2): the EF Core-backed
    /// archive/snapshot repositories (sharing <c>GoTorrentHubDbContext</c>
    /// with RSS/multi-node — see <c>AddRssRules</c>, whichever of these
    /// extensions runs first registers the DbContext), and
    /// <see cref="HistoryRecorder"/> itself, which depends on
    /// <see cref="Nodes.NodeAggregationService"/> (registered by
    /// <c>AddNodeAggregation</c> — call that first) for the actual
    /// per-node fan-out. <see cref="HistoryRecorder"/> takes a plain
    /// <see cref="HistoryOptions"/>, not <see cref="IOptions{TOptions}"/>:
    /// GoTorrent.Hub.Core deliberately has no dependency on the Options
    /// package, so the <c>IOptions&lt;HistoryOptions&gt;</c> binding
    /// machinery — and the reference it needs — stays here, in
    /// Infrastructure, unwrapped to its resolved value before Core ever
    /// sees it.
    /// </summary>
    public static IServiceCollection AddHistory(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddScoped<ITorrentHistoryRepository, TorrentHistoryRepository>();
        services.AddScoped<ISessionSnapshotRepository, SessionSnapshotRepository>();
        services.AddScoped<HistoryRecorder>();
        services.TryAddSingleton(TimeProvider.System);

        services.AddOptions<HistoryOptions>()
            .Bind(configuration.GetSection(HistoryOptions.SectionName));
        services.AddSingleton(sp => sp.GetRequiredService<IOptions<HistoryOptions>>().Value);

        return services;
    }
}
