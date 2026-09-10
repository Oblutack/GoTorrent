using System.Net;
using GoTorrent.Hub.Infrastructure.Engine;
using GoTorrent.Hub.Tests.Infrastructure;

namespace GoTorrent.Hub.Tests.Infrastructure;

public sealed class EngineClientTests
{
    // Real shapes gottrentd's own internal/api package actually produces
    // (see internal/api/torrents.go's TorrentSummary and
    // internal/api/session.go's SessionStats on the Go side) - not
    // invented for this test, so a JSON tag renamed on the Go side without
    // a matching change here is exactly what this test exists to catch.
    private const string TorrentsJson = """
        [
          {
            "infoHash": "0102030405060708090a0b0c0d0e0f1011121314",
            "name": "example.iso",
            "state": "Downloading",
            "downloaded": 1024,
            "uploaded": 512,
            "left": 2048,
            "totalLength": 3072,
            "numPieces": 3,
            "havePieces": 1,
            "peerCount": 2,
            "seedRatio": 0.5,
            "private": false,
            "category": "linux",
            "tags": ["iso", "verified"],
            "queuePosition": 0,
            "forceStart": false
          }
        ]
        """;

    private const string SessionJson = """
        {
          "torrentCount": 1,
          "downloadingCount": 1,
          "seedingCount": 0,
          "pausedCount": 0,
          "errorCount": 0,
          "totalDownloaded": 1024,
          "totalUploaded": 512,
          "totalPeerCount": 2
        }
        """;

    [Fact]
    public async Task ListTorrentsAsync_DeserializesARealGottrentdResponse()
    {
        var handler = new FakeHttpMessageHandler(HttpStatusCode.OK, TorrentsJson);
        var client = new EngineClient(new HttpClient(handler) { BaseAddress = new Uri("http://engine.local/") });

        var torrents = await client.ListTorrentsAsync(CancellationToken.None);

        var torrent = Assert.Single(torrents);
        Assert.Equal("0102030405060708090a0b0c0d0e0f1011121314", torrent.InfoHash);
        Assert.Equal("example.iso", torrent.Name);
        Assert.Equal("Downloading", torrent.State);
        Assert.Equal(1024, torrent.Downloaded);
        Assert.Equal(0.5, torrent.SeedRatio);
        Assert.Equal(["iso", "verified"], torrent.Tags);
    }

    [Fact]
    public async Task ListTorrentsAsync_EmptyFleetReturnsEmptyListNotNull()
    {
        var handler = new FakeHttpMessageHandler(HttpStatusCode.OK, "[]");
        var client = new EngineClient(new HttpClient(handler) { BaseAddress = new Uri("http://engine.local/") });

        var torrents = await client.ListTorrentsAsync(CancellationToken.None);

        Assert.Empty(torrents);
    }

    [Fact]
    public async Task GetSessionAsync_DeserializesARealGottrentdResponse()
    {
        var handler = new FakeHttpMessageHandler(HttpStatusCode.OK, SessionJson);
        var client = new EngineClient(new HttpClient(handler) { BaseAddress = new Uri("http://engine.local/") });

        var session = await client.GetSessionAsync(CancellationToken.None);

        Assert.Equal(1, session.TorrentCount);
        Assert.Equal(1, session.DownloadingCount);
        Assert.Equal(1024, session.TotalDownloaded);
    }

    [Fact]
    public async Task ListTorrentsAsync_RequestsTheExpectedPath()
    {
        var handler = new FakeHttpMessageHandler(HttpStatusCode.OK, "[]");
        var client = new EngineClient(new HttpClient(handler) { BaseAddress = new Uri("http://engine.local/") });

        await client.ListTorrentsAsync(CancellationToken.None);

        Assert.NotNull(handler.LastRequest);
        Assert.Equal("http://engine.local/api/v1/torrents", handler.LastRequest!.RequestUri!.ToString());
    }
}
