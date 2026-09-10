using GoTorrent.Hub.Core.Engine;

namespace GoTorrent.Hub.Tests.Api;

/// <summary>
/// A canned <see cref="IEngineClient"/> for integration tests that need
/// the Api project to actually boot and route a request, but have no
/// business talking to a real gottrentd - EngineClientTests already
/// covers the real HTTP/JSON behavior in isolation.
/// </summary>
public sealed class StubEngineClient : IEngineClient
{
    public static readonly TorrentSummary SampleTorrent = new(
        InfoHash: "0102030405060708090a0b0c0d0e0f1011121314",
        Name: "stub.iso",
        State: "Seeding",
        Downloaded: 100,
        Uploaded: 200,
        Left: 0,
        TotalLength: 100,
        NumPieces: 1,
        HavePieces: 1,
        PeerCount: 0,
        SeedRatio: 2.0,
        Private: false,
        Category: null,
        Tags: null,
        QueuePosition: 0,
        ForceStart: false);

    public static readonly SessionStats SampleSession = new(
        TorrentCount: 1,
        DownloadingCount: 0,
        SeedingCount: 1,
        PausedCount: 0,
        ErrorCount: 0,
        TotalDownloaded: 100,
        TotalUploaded: 200,
        TotalPeerCount: 0);

    public Task<IReadOnlyList<TorrentSummary>> ListTorrentsAsync(CancellationToken cancellationToken) =>
        Task.FromResult<IReadOnlyList<TorrentSummary>>([SampleTorrent]);

    public Task<SessionStats> GetSessionAsync(CancellationToken cancellationToken) =>
        Task.FromResult(SampleSession);
}
