using GoTorrent.Hub.Core.Rss;
using GoTorrent.Hub.Infrastructure.Persistence;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace GoTorrent.Hub.Infrastructure.Rss;

public static class DependencyInjection
{
    /// <summary>
    /// Registers the RSS auto-download feature: the Hub's own SQLite
    /// database (connection string from <paramref name="configuration"/>'s
    /// "ConnectionStrings:GoTorrentHub", defaulting to a file next to the
    /// running process — fine for dev, not meant to be the production
    /// answer, see the DbContext's own doc comment on staying
    /// provider-agnostic), the EF Core-backed repository/store, a real
    /// feed reader, and <see cref="RssPollCycleRunner"/> itself.
    /// </summary>
    public static IServiceCollection AddRssRules(this IServiceCollection services, IConfiguration configuration)
    {
        var connectionString = configuration.GetConnectionString("GoTorrentHub") ?? "Data Source=gotorrenthub.db";
        services.AddDbContext<GoTorrentHubDbContext>(options => options.UseSqlite(connectionString));

        services.AddScoped<IRssRuleRepository, RssRuleRepository>();
        services.AddScoped<IProcessedFeedItemStore, ProcessedFeedItemStore>();
        services.AddHttpClient<IRssFeedReader, SyndicationFeedReader>();
        services.AddScoped<RssPollCycleRunner>();

        services.AddOptions<RssPollingOptions>()
            .Bind(configuration.GetSection(RssPollingOptions.SectionName));

        return services;
    }
}
