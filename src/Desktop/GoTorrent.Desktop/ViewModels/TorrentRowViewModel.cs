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

    public TorrentRowViewModel(TorrentSummary summary)
    {
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
    }
}
