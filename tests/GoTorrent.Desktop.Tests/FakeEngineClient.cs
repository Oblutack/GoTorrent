using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>A controllable <see cref="IEngineClient"/> for MainViewModelTests - no real gottrentd or HTTP involved.</summary>
public sealed class FakeEngineClient : IEngineClient
{
    public List<TorrentSummary> Torrents { get; set; } = [];

    public SessionStats Session { get; set; } = new(0, 0, 0, 0, 0, 0, 0, 0);

    public Exception? Failure { get; set; }

    public Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken) =>
        Failure is not null
            ? Task.FromException<IReadOnlyList<TorrentSummary>>(Failure)
            : Task.FromResult<IReadOnlyList<TorrentSummary>>(Torrents);

    public Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken) =>
        Failure is not null ? Task.FromException<SessionStats>(Failure) : Task.FromResult(Session);
}
