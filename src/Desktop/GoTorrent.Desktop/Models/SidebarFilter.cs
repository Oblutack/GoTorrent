namespace GoTorrent.Desktop.Models;

/// <summary>
/// One entry in the torrent list's sidebar - either a built-in status
/// filter (All/Downloading/Seeding/Paused/Error) or a category pulled
/// live from whatever <see cref="TorrentSummary.Category"/> values are
/// actually present, prefixed so a category named e.g. "All" can never
/// collide with the built-in filter of the same label.
/// </summary>
public sealed record SidebarFilter(string Key, string Label)
{
    public const string AllKey = "status:all";
    public const string DownloadingKey = "status:downloading";
    public const string SeedingKey = "status:seeding";
    public const string PausedKey = "status:paused";
    public const string ErrorKey = "status:error";
    private const string CategoryPrefix = "category:";

    public static SidebarFilter Category(string name) => new(CategoryPrefix + name, name);

    /// <summary>
    /// Takes the two fields it needs directly, rather than a
    /// <see cref="TorrentSummary"/> - 6.5's stable-row-identity rework
    /// means the live torrent list is a <c>ViewModels.TorrentRowViewModel</c>,
    /// not a <see cref="TorrentSummary"/>, and this stays usable from
    /// either (or a plain test fixture) without a Models-to-ViewModels
    /// dependency in either direction.
    /// </summary>
    public bool Matches(string state, string? category) => Key switch
    {
        AllKey => true,
        DownloadingKey => state is "Downloading" or "FetchingMetadata" or "CheckingFiles",
        SeedingKey => state == "Seeding",
        PausedKey => state == "Paused",
        ErrorKey => state == "Error",
        _ when Key.StartsWith(CategoryPrefix, StringComparison.Ordinal) => category == Label,
        _ => true,
    };
}
