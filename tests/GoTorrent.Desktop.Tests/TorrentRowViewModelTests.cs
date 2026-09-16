using GoTorrent.Desktop.Models;
using GoTorrent.Desktop.ViewModels;

namespace GoTorrent.Desktop.Tests;

/// <summary>
/// Stage 6's "per-torrent speed and ETA, computed client-side" - the rate
/// math itself mirrors <c>MainViewModel.RecordSpeedSample</c>/
/// <c>RefreshPeerRatesAsync</c>'s already-tested two-samples-and-a-clock
/// technique, applied per torrent instead of fleet-wide/per-peer.
/// </summary>
public sealed class TorrentRowViewModelTests
{
    private static TorrentSummary MakeTorrent(string state, long downloaded, long uploaded, long left, long totalLength) => new(
        InfoHash: "0102030405060708090a0b0c0d0e0f1011121314",
        Name: "test.iso",
        State: state,
        Downloaded: downloaded,
        Uploaded: uploaded,
        Left: left,
        TotalLength: totalLength,
        NumPieces: 10,
        HavePieces: 1,
        PeerCount: 2,
        SeedRatio: 0,
        Private: false,
        Category: null,
        Tags: null,
        QueuePosition: 0,
        ForceStart: false);

    [Fact]
    public void FirstSample_HasZeroRateAndNoBaselineYet()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, 1000, 1000), time);

        Assert.Equal(0, row.DownloadRateKBps);
        Assert.Equal(0, row.UploadRateKBps);
    }

    [Fact]
    public void UpdateFrom_ComputesRateFromTwoSamples()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, 10240, 10240), time);

        time.Now = time.Now.AddSeconds(2);
        row.UpdateFrom(MakeTorrent("Downloading", 4096, 2048, 6144, 10240));

        // 4096 bytes over 2 seconds = 2048 B/s = 2 KiB/s.
        Assert.Equal(2.0, row.DownloadRateKBps, precision: 3);
        Assert.Equal(1.0, row.UploadRateKBps, precision: 3);
    }

    [Fact]
    public void UpdateFrom_NeverReportsANegativeRate()
    {
        // A resumed/re-verified torrent can report a lower cumulative total
        // than the previous poll (e.g. a resume-data mismatch triggering a
        // re-check) - the rate must clamp to 0, not go negative.
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 5000, 0, 5000, 10000), time);

        time.Now = time.Now.AddSeconds(1);
        row.UpdateFrom(MakeTorrent("Downloading", 1000, 0, 9000, 10000));

        Assert.Equal(0, row.DownloadRateKBps);
    }

    [Fact]
    public void Eta_IsDashOutsideDownloadingState()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Seeding", 10000, 0, 0, 10000), time);

        Assert.Equal("-", row.EtaDisplay);
    }

    [Fact]
    public void Eta_IsDashWhenNothingIsLeft()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 10000, 0, 0, 10000), time);

        Assert.Equal("-", row.EtaDisplay);
    }

    [Fact]
    public void Eta_IsInfinityWhileDownloadingWithNoMeasurableRateYet()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, 10000, 10000), time);

        Assert.Equal("∞", row.EtaDisplay);
    }

    [Fact]
    public void Eta_FormatsACompactDurationFromTheCurrentRate()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, 20480, 20480), time);

        time.Now = time.Now.AddSeconds(1);
        // 10240 bytes/s = 10 KiB/s; 10240 bytes left / 10240 B/s = 1s left.
        row.UpdateFrom(MakeTorrent("Downloading", 10240, 0, 10240, 20480));

        Assert.Equal("1s", row.EtaDisplay);
    }

    [Fact]
    public void Eta_ClampsToInfinityIfTheRateDropsBackToZeroOnALaterSample()
    {
        // Proves ETA isn't stuck at whatever the last real rate was -
        // a stalled transfer (0 B/s on the next sample) must fall back to
        // "unknown", not keep reporting a stale countdown.
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, 20480, 20480), time);

        time.Now = time.Now.AddSeconds(1);
        row.UpdateFrom(MakeTorrent("Downloading", 10240, 0, 10240, 20480));
        Assert.Equal("1s", row.EtaDisplay);

        time.Now = time.Now.AddSeconds(1);
        row.UpdateFrom(MakeTorrent("Downloading", 10240, 0, 10240, 20480));
        Assert.Equal("∞", row.EtaDisplay);
    }

    [Fact]
    public void Eta_FallsBackToInfinityRatherThanOverflowingOnAnExtremeDuration()
    {
        // A real regression guard, not a hypothetical: a huge file
        // (near long.MaxValue left) crawling at a near-zero but still
        // positive rate divides out to a duration TimeSpan.FromSeconds
        // can't represent (its own ceiling is ~29,000 years) and used to
        // throw OverflowException straight out of UpdateFrom - caught by
        // this test, not live.
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, long.MaxValue - 1024, long.MaxValue), time);

        time.Now = time.Now.AddSeconds(1);
        row.UpdateFrom(MakeTorrent("Downloading", 1024, 0, long.MaxValue - 2048, long.MaxValue));

        Assert.Equal("∞", row.EtaDisplay);
    }

    [Fact]
    public void RateHistory_StaysEmptyOnTheFirstSample()
    {
        // Mirrors MainViewModel.RecordSpeedSample's own "first sample has
        // no elapsed baseline, so nothing gets appended yet" behavior -
        // the sparkline shouldn't show a fabricated leading zero before
        // any real rate has actually been measured.
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, 10000, 10000), time);

        Assert.Empty(row.DownloadRateHistory);
        Assert.Empty(row.UploadRateHistory);
    }

    [Fact]
    public void RateHistory_AppendsOneSampleForEachRealUpdate()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, 30720, 30720), time);

        time.Now = time.Now.AddSeconds(1);
        row.UpdateFrom(MakeTorrent("Downloading", 1024, 0, 29696, 30720));
        time.Now = time.Now.AddSeconds(1);
        row.UpdateFrom(MakeTorrent("Downloading", 3072, 0, 27648, 30720));

        Assert.Equal(2, row.DownloadRateHistory.Count);
        Assert.Equal(1.0, row.DownloadRateHistory[0], precision: 3);
        Assert.Equal(2.0, row.DownloadRateHistory[1], precision: 3);
    }

    [Fact]
    public void RateHistory_DropsTheOldestSampleOnceItExceedsTheCap()
    {
        var time = new FixedTimeProvider(DateTimeOffset.UtcNow);
        var row = new TorrentRowViewModel(MakeTorrent("Downloading", 0, 0, long.MaxValue / 2, long.MaxValue), time);

        long downloaded = 0;
        for (var i = 0; i < 45; i++)
        {
            time.Now = time.Now.AddSeconds(1);
            downloaded += 1024;
            row.UpdateFrom(MakeTorrent("Downloading", downloaded, 0, long.MaxValue - downloaded, long.MaxValue));
        }

        // 45 real updates against a 40-sample cap - the oldest 5 must have
        // been trimmed from the front, not the collection growing without
        // bound.
        Assert.Equal(40, row.DownloadRateHistory.Count);
    }
}
