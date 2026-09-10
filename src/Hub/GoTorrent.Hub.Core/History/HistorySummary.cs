namespace GoTorrent.Hub.Core.History;

/// <summary>Aggregate rollup over the whole completed-torrent archive — computed DB-side, never by pulling every row into memory.</summary>
public sealed record HistorySummary(int CompletedCount, long TotalDownloaded, long TotalUploaded);
