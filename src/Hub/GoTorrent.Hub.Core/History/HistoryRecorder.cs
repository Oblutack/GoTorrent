using GoTorrent.Hub.Core.Nodes;
using Microsoft.Extensions.Logging;

namespace GoTorrent.Hub.Core.History;

/// <summary>
/// One recording pass: snapshot every reachable enabled node's session
/// stats (the timeline's raw material), archive any torrent observed in
/// gottrentd's <c>Seeding</c> state that isn't already archived for that
/// node, then prune snapshots past <see cref="HistoryOptions.SnapshotRetention"/>.
/// Deliberately independent of however it's scheduled (a
/// <c>BackgroundService</c> on a timer, in this project's case) — same
/// "the logic is unit-testable with fakes, no real timer/DB/HTTP needed"
/// shape as <c>RssPollCycleRunner</c>. Reuses <see cref="NodeAggregationService"/>
/// for the actual per-node fan-out and failure tolerance rather than a
/// third copy of that logic — one node being unreachable already just
/// means it's absent from what this recorder sees, not a failure here.
/// </summary>
public sealed class HistoryRecorder(
    NodeAggregationService aggregation,
    ITorrentHistoryRepository history,
    ISessionSnapshotRepository snapshots,
    HistoryOptions options,
    TimeProvider timeProvider,
    ILogger<HistoryRecorder> logger)
{
    public async Task RecordAsync(CancellationToken cancellationToken)
    {
        var now = timeProvider.GetUtcNow();

        var statuses = await aggregation.GetNodeStatusesAsync(cancellationToken);
        foreach (var status in statuses)
        {
            if (!status.Reachable || status.Session is null)
            {
                continue;
            }
            await snapshots.AddAsync(new SessionSnapshot
            {
                NodeId = status.NodeId,
                NodeName = status.NodeName,
                CapturedAt = now,
                TorrentCount = status.Session.TorrentCount,
                DownloadingCount = status.Session.DownloadingCount,
                SeedingCount = status.Session.SeedingCount,
                TotalDownloaded = status.Session.TotalDownloaded,
                TotalUploaded = status.Session.TotalUploaded,
                TotalPeerCount = status.Session.TotalPeerCount,
            }, cancellationToken);
        }

        var torrents = await aggregation.GetAggregatedTorrentsAsync(cancellationToken);
        foreach (var entry in torrents)
        {
            // State == "Seeding" is gottrentd's own state-machine
            // definition of "fully downloaded" - deliberately not
            // Left == 0, which a magnet still in FetchingMetadata also
            // satisfies (nothing is known yet, so there's nothing marked
            // as remaining either) and would otherwise be archived the
            // instant it was added, long before anything actually
            // downloaded.
            if (entry.Torrent.State != "Seeding")
            {
                continue;
            }
            if (await history.ExistsAsync(entry.NodeId, entry.Torrent.InfoHash, cancellationToken))
            {
                continue;
            }
            await history.AddAsync(new TorrentHistoryEntry
            {
                NodeId = entry.NodeId,
                NodeName = entry.NodeName,
                InfoHash = entry.Torrent.InfoHash,
                Name = entry.Torrent.Name,
                Category = entry.Torrent.Category,
                TotalLength = entry.Torrent.TotalLength,
                Downloaded = entry.Torrent.Downloaded,
                Uploaded = entry.Torrent.Uploaded,
                SeedRatio = entry.Torrent.SeedRatio,
                CompletedAt = now,
            }, cancellationToken);
            logger.LogInformation("Archived completed torrent {Name} from node {NodeName}", entry.Torrent.Name, entry.NodeName);
        }

        var cutoff = now - options.SnapshotRetention;
        var pruned = await snapshots.PruneOlderThanAsync(cutoff, cancellationToken);
        if (pruned > 0)
        {
            logger.LogInformation("Pruned {Count} session snapshots older than {Cutoff}", pruned, cutoff);
        }
    }
}
