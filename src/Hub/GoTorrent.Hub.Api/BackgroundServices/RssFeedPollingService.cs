using GoTorrent.Hub.Core.Rss;
using Microsoft.Extensions.Options;

namespace GoTorrent.Hub.Api.BackgroundServices;

/// <summary>
/// Hosting glue only — runs <see cref="RssPollCycleRunner"/> once
/// immediately on startup and then again every
/// <see cref="RssPollingOptions.Interval"/>. All the actual "what does a
/// poll cycle do" logic lives in <see cref="RssPollCycleRunner"/> itself,
/// specifically so it can be unit-tested without a real timer or a hosted
/// service anywhere near the test — see its own doc comment.
/// </summary>
public sealed class RssFeedPollingService(
    IServiceScopeFactory scopeFactory,
    IOptions<RssPollingOptions> options,
    ILogger<RssFeedPollingService> logger) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        using var timer = new PeriodicTimer(options.Value.Interval);
        do
        {
            await RunOneCycleAsync(stoppingToken);
        } while (await timer.WaitForNextTickAsync(stoppingToken));
    }

    private async Task RunOneCycleAsync(CancellationToken stoppingToken)
    {
        try
        {
            // RssPollCycleRunner's own dependencies (the DbContext behind
            // IRssRuleRepository/IProcessedFeedItemStore, chiefly) are
            // scoped - BackgroundService itself is a singleton, so a
            // fresh scope per cycle is required, not optional.
            using var scope = scopeFactory.CreateScope();
            var runner = scope.ServiceProvider.GetRequiredService<RssPollCycleRunner>();
            await runner.RunAsync(stoppingToken);
        }
        catch (Exception ex) when (ex is not OperationCanceledException)
        {
            logger.LogError(ex, "RSS poll cycle failed");
        }
    }
}
