namespace GoTorrent.Desktop.Models;

/// <summary>
/// Mirrors the Hub's <c>GET /api/v1/history/summary</c> response
/// (<c>GoTorrent.Hub.Core.History.HistorySummary</c>) - an aggregate
/// rollup computed DB-side on the Hub, not derived client-side from
/// <see cref="ActivityHistoryEntry"/> rows (which are paged, so a
/// client-side sum would be wrong once the archive exceeds one page).
/// </summary>
public sealed record ActivityHistorySummary(int CompletedCount, long TotalDownloaded, long TotalUploaded);
