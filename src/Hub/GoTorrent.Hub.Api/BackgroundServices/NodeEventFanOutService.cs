using GoTorrent.Hub.Core.Events;
using GoTorrent.Hub.Core.Nodes;
using Microsoft.Extensions.Options;

namespace GoTorrent.Hub.Api.BackgroundServices;

/// <summary>
/// Hosting glue only, same shape as <see cref="RssFeedPollingService"/>/
/// <see cref="HistoryRecordingService"/>: re-reads the registered node
/// set on a timer and hands it to <see cref="NodeEventFanOutCoordinator.Sync"/>,
/// which does the actual work of starting/stopping per-node
/// subscriptions. Unlike those two, the coordinator's own subscriptions
/// keep running *between* ticks — this loop exists to notice a node
/// being added, removed, or toggled, not to redo work each pass.
/// </summary>
public sealed class NodeEventFanOutService(
    IServiceScopeFactory scopeFactory,
    NodeEventFanOutCoordinator coordinator,
    IOptions<NodeEventFanOutOptions> options,
    ILogger<NodeEventFanOutService> logger) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        using var timer = new PeriodicTimer(options.Value.SyncInterval);
        do
        {
            await RunOneSyncAsync(stoppingToken);
        } while (await timer.WaitForNextTickAsync(stoppingToken));
    }

    public override async Task StopAsync(CancellationToken cancellationToken)
    {
        coordinator.StopAll();
        await base.StopAsync(cancellationToken);
    }

    private async Task RunOneSyncAsync(CancellationToken stoppingToken)
    {
        try
        {
            using var scope = scopeFactory.CreateScope();
            var nodes = scope.ServiceProvider.GetRequiredService<IEngineNodeRepository>();
            var enabledNodes = (await nodes.GetAllAsync(stoppingToken)).Where(n => n.Enabled).ToList();
            coordinator.Sync(enabledNodes, stoppingToken);
        }
        catch (Exception ex) when (ex is not OperationCanceledException)
        {
            logger.LogError(ex, "Node event fan-out sync failed");
        }
    }
}
