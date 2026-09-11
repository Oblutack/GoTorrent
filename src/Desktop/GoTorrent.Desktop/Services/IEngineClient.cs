using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// Desktop's seam onto a single <c>gottrentd</c> node (Phase 4's Go
/// daemon). Deliberately thin today, growing method by method as the UI
/// actually needs each one — same "don't build a full proxy up front"
/// discipline the Hub's own <c>IEngineClient</c> already follows.
/// </summary>
public interface IEngineClient
{
    Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken);

    Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken);

    Task<string> AddMagnetAsync(string magnet, string? category, string? downloadDir, CancellationToken cancellationToken);

    Task<string> AddTorrentFileAsync(byte[] fileBytes, string fileName, string? category, string? downloadDir, CancellationToken cancellationToken);

    Task PauseAsync(string infoHash, CancellationToken cancellationToken);

    Task ResumeAsync(string infoHash, CancellationToken cancellationToken);

    Task DeleteAsync(string infoHash, bool deleteData, CancellationToken cancellationToken);

    Task<TorrentDetail> GetTorrentDetailAsync(string infoHash, CancellationToken cancellationToken);

    Task<IReadOnlyList<FileEntry>> GetFilesAsync(string infoHash, CancellationToken cancellationToken);

    Task<IReadOnlyList<PeerEntry>> GetPeersAsync(string infoHash, CancellationToken cancellationToken);

    Task<IReadOnlyList<TrackerEntry>> GetTrackersAsync(string infoHash, CancellationToken cancellationToken);

    /// <summary>Reads the fleet-wide rate limits currently in effect (an empty PATCH body changes nothing, just reports them back).</summary>
    Task<SessionLimits> GetSessionLimitsAsync(CancellationToken cancellationToken);

    Task<SessionLimits> SetSessionLimitsAsync(long? downLimitKB, long? upLimitKB, CancellationToken cancellationToken);
}
