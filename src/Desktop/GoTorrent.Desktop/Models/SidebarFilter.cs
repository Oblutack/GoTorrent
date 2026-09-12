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

    public bool Matches(TorrentSummary torrent) => Key switch
    {
        AllKey => true,
        DownloadingKey => torrent.State is "Downloading" or "FetchingMetadata" or "CheckingFiles",
        SeedingKey => torrent.State == "Seeding",
        PausedKey => torrent.State == "Paused",
        ErrorKey => torrent.State == "Error",
        _ when Key.StartsWith(CategoryPrefix, StringComparison.Ordinal) => torrent.Category == Label,
        _ => true,
    };
}
