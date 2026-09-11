using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>A controllable <see cref="IEngineClient"/> for MainViewModelTests - no real gottrentd or HTTP involved.</summary>
public sealed class FakeEngineClient : IEngineClient
{
    public List<TorrentSummary> Torrents { get; set; } = [];

    public SessionStats Session { get; set; } = new(0, 0, 0, 0, 0, 0, 0, 0);

    public Exception? Failure { get; set; }

    public List<string> PausedHashes { get; } = [];
    public List<string> ResumedHashes { get; } = [];
    public List<(string InfoHash, bool DeleteData)> DeletedHashes { get; } = [];
    public string? LastAddedMagnet { get; private set; }
    public string? LastAddedFileName { get; private set; }

    public TorrentDetail? Detail { get; set; }
    public List<FileEntry> Files { get; set; } = [];
    public List<PeerEntry> Peers { get; set; } = [];
    public List<TrackerEntry> Trackers { get; set; } = [];
    public SessionLimits SessionLimits { get; set; } = new(0, 0);
    public List<string> DetailRequestedHashes { get; } = [];
    public PiecesInfo Pieces { get; set; } = new(0, 0, []);

    public Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken) =>
        Failure is not null
            ? Task.FromException<IReadOnlyList<TorrentSummary>>(Failure)
            : Task.FromResult<IReadOnlyList<TorrentSummary>>(Torrents);

    public Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken) =>
        Failure is not null ? Task.FromException<SessionStats>(Failure) : Task.FromResult(Session);

    public Task<string> AddMagnetAsync(string magnet, string? category, string? downloadDir, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException<string>(Failure);
        }
        LastAddedMagnet = magnet;
        return Task.FromResult("0102030405060708090a0b0c0d0e0f1011121314");
    }

    public Task<string> AddTorrentFileAsync(byte[] fileBytes, string fileName, string? category, string? downloadDir, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException<string>(Failure);
        }
        LastAddedFileName = fileName;
        return Task.FromResult("0102030405060708090a0b0c0d0e0f1011121314");
    }

    public Task PauseAsync(string infoHash, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException(Failure);
        }
        PausedHashes.Add(infoHash);
        return Task.CompletedTask;
    }

    public Task ResumeAsync(string infoHash, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException(Failure);
        }
        ResumedHashes.Add(infoHash);
        return Task.CompletedTask;
    }

    public Task DeleteAsync(string infoHash, bool deleteData, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException(Failure);
        }
        DeletedHashes.Add((infoHash, deleteData));
        return Task.CompletedTask;
    }

    public Task<TorrentDetail> GetTorrentDetailAsync(string infoHash, CancellationToken cancellationToken)
    {
        DetailRequestedHashes.Add(infoHash);
        if (Failure is not null)
        {
            return Task.FromException<TorrentDetail>(Failure);
        }
        return Task.FromResult(Detail ?? throw new InvalidOperationException("Detail was not set on the fake."));
    }

    public Task<IReadOnlyList<FileEntry>> GetFilesAsync(string infoHash, CancellationToken cancellationToken) =>
        Failure is not null
            ? Task.FromException<IReadOnlyList<FileEntry>>(Failure)
            : Task.FromResult<IReadOnlyList<FileEntry>>(Files);

    public Task<IReadOnlyList<PeerEntry>> GetPeersAsync(string infoHash, CancellationToken cancellationToken) =>
        Failure is not null
            ? Task.FromException<IReadOnlyList<PeerEntry>>(Failure)
            : Task.FromResult<IReadOnlyList<PeerEntry>>(Peers);

    public Task<IReadOnlyList<TrackerEntry>> GetTrackersAsync(string infoHash, CancellationToken cancellationToken) =>
        Failure is not null
            ? Task.FromException<IReadOnlyList<TrackerEntry>>(Failure)
            : Task.FromResult<IReadOnlyList<TrackerEntry>>(Trackers);

    public Task<PiecesInfo> GetPiecesAsync(string infoHash, CancellationToken cancellationToken) =>
        Failure is not null ? Task.FromException<PiecesInfo>(Failure) : Task.FromResult(Pieces);

    public Task<SessionLimits> GetSessionLimitsAsync(CancellationToken cancellationToken) =>
        Failure is not null ? Task.FromException<SessionLimits>(Failure) : Task.FromResult(SessionLimits);

    public Task<SessionLimits> SetSessionLimitsAsync(long? downLimitKB, long? upLimitKB, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException<SessionLimits>(Failure);
        }
        SessionLimits = new SessionLimits(downLimitKB ?? SessionLimits.DownLimitKB, upLimitKB ?? SessionLimits.UpLimitKB);
        return Task.FromResult(SessionLimits);
    }
}
