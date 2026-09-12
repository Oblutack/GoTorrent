using System.ComponentModel;

namespace GoTorrent.Desktop.Models;

/// <summary>
/// One entry in the torrent list's sidebar - a built-in status filter
/// (All/Downloading/Seeding/Paused/Error), a category, or a tag, the
/// latter two pulled live from whatever values are actually present on
/// <see cref="Torrents"/> (see <c>MainViewModel.ApplyFilter</c>) and
/// prefixed so e.g. a category and a tag both named "Movies" can't
/// collide with each other or with a built-in filter of the same label.
/// Implements <see cref="INotifyPropertyChanged"/> by hand (rather than
/// pulling <c>CommunityToolkit.Mvvm</c> into this otherwise-plain Models
/// project, the way <c>ViewModels.TorrentRowViewModel</c> does) purely so
/// <see cref="Count"/> can update the bound sidebar label live - a plain
/// mutable property on a reused instance is invisible to Avalonia's
/// binding layer without this.
/// </summary>
public sealed record SidebarFilter(string Key, string Label) : INotifyPropertyChanged
{
    public const string AllKey = "status:all";
    public const string DownloadingKey = "status:downloading";
    public const string SeedingKey = "status:seeding";
    public const string PausedKey = "status:paused";
    public const string ErrorKey = "status:error";
    private const string CategoryPrefix = "category:";
    private const string TagPrefix = "tag:";

    public static SidebarFilter Category(string name) => new(CategoryPrefix + name, name);

    public static SidebarFilter Tag(string name) => new(TagPrefix + name, name);

    /// <summary>
    /// How many currently-displayed torrents this entry matches - set by
    /// <c>MainViewModel.ApplyFilter</c> after reconciling the sidebar.
    /// Deliberately excluded from equality below.
    /// </summary>
    private int _count;

    public int Count
    {
        get => _count;
        set
        {
            if (_count == value)
            {
                return;
            }
            _count = value;
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(nameof(Count)));
        }
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>
    /// A real gotcha, confirmed by direct testing rather than assumed: a
    /// C# record's compiler-synthesized <c>Equals</c>/<c>GetHashCode</c>/
    /// <c>ToString</c> include <i>every</i> public instance property, not
    /// just the primary constructor's positional ones - <see cref="Count"/>,
    /// declared in the body with its own explicit backing field, was still
    /// silently part of the generated equality until this override was
    /// added. That broke two things at once, both caught by real (not
    /// hypothetical) failing tests the moment <see cref="Count"/> started
    /// actually being mutated: <c>SyncCollection</c>'s reference-preserving
    /// reconciliation (a filter whose <c>Count</c> just changed stopped
    /// being "equal" to its own freshly-reconstructed candidate, so the
    /// live, still-selected instance got removed and replaced instead of
    /// kept), and <c>ApplyFilter</c>'s own "is the live <c>SelectedFilter</c>
    /// still a real entry in <c>SidebarFilters</c>" check (a test-constructed
    /// <c>SidebarFilter</c> with <c>Count</c> at its default 0 no longer
    /// matched the real, already-counted instance in the collection).
    /// Manually overriding both members to compare on <see cref="Key"/>/
    /// <see cref="Label"/> only is what actually makes this class's
    /// documented "identity is Key+Label, Count is just live display
    /// state" contract true, not just stated.
    /// </summary>
    public bool Equals(SidebarFilter? other) => other is not null && Key == other.Key && Label == other.Label;

    public override int GetHashCode() => Key.GetHashCode();

    /// <summary>
    /// Takes the fields it needs directly, rather than a
    /// <see cref="TorrentSummary"/> - 6.5's stable-row-identity rework
    /// means the live torrent list is a <c>ViewModels.TorrentRowViewModel</c>,
    /// not a <see cref="TorrentSummary"/>, and this stays usable from
    /// either (or a plain test fixture) without a Models-to-ViewModels
    /// dependency in either direction.
    /// </summary>
    public bool Matches(string state, string? category, IReadOnlyList<string>? tags) => Key switch
    {
        AllKey => true,
        DownloadingKey => state is "Downloading" or "FetchingMetadata" or "CheckingFiles",
        SeedingKey => state == "Seeding",
        PausedKey => state == "Paused",
        ErrorKey => state == "Error",
        _ when Key.StartsWith(CategoryPrefix, StringComparison.Ordinal) => category == Label,
        _ when Key.StartsWith(TagPrefix, StringComparison.Ordinal) => tags is not null && tags.Contains(Label),
        _ => true,
    };
}
