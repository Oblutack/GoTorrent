using CommunityToolkit.Mvvm.ComponentModel;
using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.ViewModels;

/// <summary>
/// Mutable, per-torrent row state with a stable identity (<see cref="InfoHash"/>).
/// <see cref="MainViewModel.Torrents"/> holds one of these per torrent for
/// as long as gottrentd reports it, updating its properties in place from
/// each fresh <see cref="TorrentSummary"/> poll (<see cref="UpdateFrom"/>)
/// rather than ever replacing the object.
///
/// <para>
/// This is the 6.5 fix for a real bug: <see cref="TorrentSummary"/> is a
/// <c>record</c> (value equality), and <c>[ObservableProperty]</c>'s
/// generated setter skips both the assignment and the
/// <c>PropertyChanged</c> notification when the new value is "equal" to
/// the old one. For an idle torrent whose fields genuinely don't change
/// between two 2-second polls, reassigning <c>SelectedTorrent</c> to the
/// freshly-fetched (but distinct-by-reference) record was silently a
/// no-op - leaving the property pointing at an instance no longer present
/// in the freshly-rebuilt <c>DisplayedTorrents</c> collection, so the
/// <c>DataGrid</c> showed no selection despite <c>SelectedTorrent</c>
/// still being "the same torrent" by value. A mutable class has ordinary
/// reference identity: the same <see cref="TorrentRowViewModel"/> instance
/// stays selected across a refresh because it never gets replaced,
/// full stop.
/// </para>
/// </summary>
public sealed partial class TorrentRowViewModel : ViewModelBase
{
    public string InfoHash { get; }

    [ObservableProperty]
    public partial string Name { get; set; }

    [ObservableProperty]
    public partial string State { get; set; }

    [ObservableProperty]
    public partial long Downloaded { get; set; }

    [ObservableProperty]
    public partial long Uploaded { get; set; }

    [ObservableProperty]
    public partial long Left { get; set; }

    [ObservableProperty]
    public partial long TotalLength { get; set; }

    [ObservableProperty]
    public partial int NumPieces { get; set; }

    [ObservableProperty]
    public partial int HavePieces { get; set; }

    [ObservableProperty]
    public partial int PeerCount { get; set; }

    [ObservableProperty]
    public partial double SeedRatio { get; set; }

    [ObservableProperty]
    public partial bool Private { get; set; }

    [ObservableProperty]
    public partial string? Category { get; set; }

    [ObservableProperty]
    public partial IReadOnlyList<string>? Tags { get; set; }

    [ObservableProperty]
    public partial int QueuePosition { get; set; }

    [ObservableProperty]
    public partial bool ForceStart { get; set; }

    /// <summary>Mirrors <see cref="TorrentSummary.ProgressFraction"/> - a plain computed get-only property wouldn't raise its own PropertyChanged when Left/TotalLength change, so it's set explicitly in <see cref="UpdateFrom"/> instead.</summary>
    [ObservableProperty]
    public partial double ProgressFraction { get; set; }

    /// <summary>
    /// Down/up rate in KiB/s, derived client-side the same way
    /// <c>MainViewModel.RecordSpeedSample</c> (fleet-wide) and
    /// <c>RefreshPeerRatesAsync</c> (per-peer) already turn two consecutive
    /// cumulative totals into a rate - gottrentd's <c>TorrentSummary</c>
    /// has no per-torrent rate field at all, only running
    /// Downloaded/Uploaded totals, so this needs zero Go-side changes.
    /// 0 until a second sample has actually arrived (no baseline yet),
    /// matching the fleet graph's and peer rows' own "first sample" behavior.
    /// </summary>
    [ObservableProperty]
    public partial double DownloadRateKBps { get; set; }

    [ObservableProperty]
    public partial double UploadRateKBps { get; set; }

    /// <summary>
    /// A formatted, already-invariant-culture ETA string ("-" not
    /// applicable, "∞" downloading but no measurable rate yet, otherwise a
    /// compact duration) - a string rather than a bound
    /// <c>TimeSpan</c>/double + XAML <c>StringFormat</c>, the same fix this
    /// project already applied to <c>SessionRatioDisplay</c> for the
    /// identical reason: <c>StringFormat</c> is locale-sensitive (a
    /// comma-decimal machine renders "0,50" instead of "0.50"), and
    /// <c>double.PositiveInfinity</c>/an unformattable case needs its own
    /// explicit handling a bare numeric binding can't express anyway.
    /// </summary>
    [ObservableProperty]
    public partial string EtaDisplay { get; set; } = "-";

    private readonly TimeProvider _timeProvider;
    private DateTimeOffset? _lastSampleTime;
    private long _lastDownloaded;
    private long _lastUploaded;

    public TorrentRowViewModel(TorrentSummary summary) : this(summary, TimeProvider.System)
    {
    }

    public TorrentRowViewModel(TorrentSummary summary, TimeProvider timeProvider)
    {
        _timeProvider = timeProvider;
        InfoHash = summary.InfoHash;
        Name = summary.Name;
        State = summary.State;
        UpdateFrom(summary);
    }

    /// <summary>
    /// Refreshes every mutable field from a freshly-fetched summary for
    /// the same torrent (<see cref="InfoHash"/> is assumed unchanged -
    /// callers key by it). Each <c>[ObservableProperty]</c> setter already
    /// skips a redundant same-value assignment (and the PropertyChanged it
    /// would otherwise raise), so calling this every poll costs nothing
    /// extra for fields that didn't actually change.
    /// </summary>
    public void UpdateFrom(TorrentSummary summary)
    {
        Name = summary.Name;
        State = summary.State;
        Downloaded = summary.Downloaded;
        Uploaded = summary.Uploaded;
        Left = summary.Left;
        TotalLength = summary.TotalLength;
        NumPieces = summary.NumPieces;
        HavePieces = summary.HavePieces;
        PeerCount = summary.PeerCount;
        SeedRatio = summary.SeedRatio;
        Private = summary.Private;
        Category = summary.Category;
        Tags = summary.Tags;
        QueuePosition = summary.QueuePosition;
        ForceStart = summary.ForceStart;
        ProgressFraction = summary.ProgressFraction;

        var now = _timeProvider.GetUtcNow();
        if (_lastSampleTime is { } last)
        {
            var elapsedSeconds = (now - last).TotalSeconds;
            if (elapsedSeconds > 0)
            {
                DownloadRateKBps = Math.Max(0, (summary.Downloaded - _lastDownloaded) / elapsedSeconds / 1024.0);
                UploadRateKBps = Math.Max(0, (summary.Uploaded - _lastUploaded) / elapsedSeconds / 1024.0);
            }
        }
        _lastDownloaded = summary.Downloaded;
        _lastUploaded = summary.Uploaded;
        _lastSampleTime = now;

        EtaDisplay = ComputeEtaDisplay(summary.State, summary.Left, DownloadRateKBps);
    }

    /// <summary>
    /// "-" outside <c>Downloading</c> (a completed, seeding, paused, or
    /// still-verifying/fetching-metadata torrent has no download ETA to
    /// show) or with nothing left; "∞" while downloading but with no
    /// measurable rate yet (no second sample, or a genuinely stalled
    /// transfer); otherwise <paramref name="left"/> divided by the current
    /// rate, formatted compactly.
    /// </summary>
    private static string ComputeEtaDisplay(string state, long left, double downloadRateKBps)
    {
        if (state != "Downloading" || left <= 0)
        {
            return "-";
        }
        if (downloadRateKBps <= 0)
        {
            return "∞";
        }
        return FormatDuration(TimeSpan.FromSeconds(left / (downloadRateKBps * 1024.0)));
    }

    private static string FormatDuration(TimeSpan span)
    {
        if (span.TotalDays >= 1)
        {
            return FormattableString.Invariant($"{(int)span.TotalDays}d {span.Hours}h");
        }
        if (span.TotalHours >= 1)
        {
            return FormattableString.Invariant($"{(int)span.TotalHours}h {span.Minutes}m");
        }
        if (span.TotalMinutes >= 1)
        {
            return FormattableString.Invariant($"{(int)span.TotalMinutes}m {span.Seconds}s");
        }
        return FormattableString.Invariant($"{Math.Max(0, (int)span.TotalSeconds)}s");
    }
}
