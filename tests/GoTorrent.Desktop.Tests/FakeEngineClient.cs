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
}
