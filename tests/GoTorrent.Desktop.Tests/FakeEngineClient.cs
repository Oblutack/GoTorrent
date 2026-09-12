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
    public string? LastAddedUrl { get; private set; }

    public TorrentDetail? Detail { get; set; }
    public List<FileEntry> Files { get; set; } = [];
    public List<PeerEntry> Peers { get; set; } = [];
    public List<TrackerEntry> Trackers { get; set; } = [];
    public SessionLimits SessionLimits { get; set; } = new(0, 0);
    public List<string> DetailRequestedHashes { get; } = [];
    public PiecesInfo Pieces { get; set; } = new(0, 0, []);

    /// <summary>Per-hash override for <see cref="GetTorrentDetailAsync"/>'s result - lets a test give two different selected torrents distinguishable responses, which the single shared <see cref="Detail"/> can't.</summary>
    public Dictionary<string, TorrentDetail> DetailsByHash { get; } = [];

    /// <summary>
    /// Per-hash gate for <see cref="GetTorrentDetailAsync"/> - a test can
    /// register a never-completed <see cref="TaskCompletionSource"/> here
    /// to simulate "this one call is slow," without a real sleep, to
    /// deterministically test cancelling a superseded
    /// <c>LoadSelectedDetailAsync</c> call (the stale-response race 6.5
    /// fixed). Registering the passed <see cref="CancellationToken"/>
    /// against the gate is what actually lets the real cancellation path
    /// (not just "the test never awaits the slow task") be exercised.
    /// </summary>
    public Dictionary<string, TaskCompletionSource> DetailGatesByHash { get; } = [];

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

    public Task<string> AddUrlAsync(string url, string? category, string? downloadDir, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException<string>(Failure);
        }
        LastAddedUrl = url;
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

    public List<string> VerifiedHashes { get; } = [];
    public List<string> ReannouncedHashes { get; } = [];

    public Task VerifyAsync(string infoHash, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException(Failure);
        }
        VerifiedHashes.Add(infoHash);
        return Task.CompletedTask;
    }

    public Task ReannounceAsync(string infoHash, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException(Failure);
        }
        ReannouncedHashes.Add(infoHash);
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

    public async Task<TorrentDetail> GetTorrentDetailAsync(string infoHash, CancellationToken cancellationToken)
    {
        DetailRequestedHashes.Add(infoHash);
        if (DetailGatesByHash.TryGetValue(infoHash, out var gate))
        {
            await using var registration = cancellationToken.Register(() => gate.TrySetCanceled(cancellationToken));
            await gate.Task;
        }
        if (Failure is not null)
        {
            throw Failure;
        }
        if (DetailsByHash.TryGetValue(infoHash, out var detail))
        {
            return detail;
        }
        return Detail ?? throw new InvalidOperationException("Detail was not set on the fake.");
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

    public List<(string InfoHash, string Url)> AddedTrackers { get; } = [];

    /// <summary>Every <see cref="PatchTorrentAsync"/> call's (hash, options) pair, in order - the only way a test can see a field with no corresponding <see cref="TorrentSummary"/> property to merge into (e.g. <see cref="PatchTorrentOptions.DownLimitKB"/>, which gottrentd never reports back either).</summary>
    public List<(string InfoHash, PatchTorrentOptions Options)> PatchRequests { get; } = [];

    public Task<TorrentSummary> PatchTorrentAsync(string infoHash, PatchTorrentOptions options, CancellationToken cancellationToken)
    {
        PatchRequests.Add((infoHash, options));
        if (Failure is not null)
        {
            return Task.FromException<TorrentSummary>(Failure);
        }
        var index = Torrents.FindIndex(t => t.InfoHash == infoHash);
        if (index < 0)
        {
            return Task.FromException<TorrentSummary>(new InvalidOperationException($"unknown torrent {infoHash}"));
        }
        var updated = Torrents[index] with
        {
            Category = options.Category ?? Torrents[index].Category,
            Tags = options.Tags ?? Torrents[index].Tags,
            QueuePosition = options.QueuePosition ?? Torrents[index].QueuePosition,
            ForceStart = options.ForceStart ?? Torrents[index].ForceStart,
        };
        Torrents[index] = updated;
        return Task.FromResult(updated);
    }

    public Task AddTrackerAsync(string infoHash, string url, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException(Failure);
        }
        AddedTrackers.Add((infoHash, url));
        return Task.CompletedTask;
    }

    public List<(string InfoHash, int FileIndex, string Priority)> SetFilePriorities { get; } = [];

    public Task SetFilePriorityAsync(string infoHash, int fileIndex, string priority, CancellationToken cancellationToken)
    {
        if (Failure is not null)
        {
            return Task.FromException(Failure);
        }
        SetFilePriorities.Add((infoHash, fileIndex, priority));
        return Task.CompletedTask;
    }
}
