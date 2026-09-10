using GoTorrent.Hub.Core.History;
using Microsoft.Extensions.Options;

namespace GoTorrent.Hub.Api.BackgroundServices;

/// <summary>
/// Hosting glue only — runs <see cref="HistoryRecorder"/> once
/// immediately on startup and then again every
/// <see cref="HistoryOptions.Interval"/>. Same shape as
/// <see cref="RssFeedPollingService"/>: all the actual "what does a
/// recording pass do" logic lives in <see cref="HistoryRecorder"/>
/// itself.
/// </summary>
public sealed class HistoryRecordingService(
    IServiceScopeFactory scopeFactory,
    IOptions<HistoryOptions> options,
    ILogger<HistoryRecordingService> logger) : BackgroundService
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
            using var scope = scopeFactory.CreateScope();
            var recorder = scope.ServiceProvider.GetRequiredService<HistoryRecorder>();
            await recorder.RecordAsync(stoppingToken);
        }
        catch (Exception ex) when (ex is not OperationCanceledException)
        {
            logger.LogError(ex, "History recording pass failed");
        }
    }
}
