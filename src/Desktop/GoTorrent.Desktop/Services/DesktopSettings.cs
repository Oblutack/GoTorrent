namespace GoTorrent.Desktop.Services;

/// <summary>
/// Which gottrentd to connect to, plus app-behavior preferences,
/// remembered between runs. See <see cref="ISettingsStore"/> for where
/// this is persisted. <see cref="StartMinimized"/> (and the window
/// geometry fields added alongside it for Stage 3) live here rather
/// than their own file - it's the only local-preference persistence this
/// app has, and a second settings file isn't worth the extra seam.
/// </summary>
/// <param name="WindowWidth">
/// Null means "no saved geometry yet" (first run, or a settings file
/// from before this field existed) - <c>MainWindow</c> only overrides
/// its XAML-declared default size/position when every one of
/// <see cref="WindowWidth"/>/<see cref="WindowHeight"/> is present, and
/// only ever from the window's own last <b>Normal</b>-state bounds, never
/// its maximized bounds - see <c>MainWindow.SaveGeometry</c>.
/// </param>
/// <param name="DetailSplitFraction">
/// The torrent-list/detail-pane <c>GridSplitter</c>'s position, as the
/// top row's fraction of the split grid's total height (0..1). Null
/// means "use the XAML-declared 2*/3* default".
/// </param>
/// <param name="HiddenColumns">
/// Stage 4's column chooser - the header text of each optional torrent-
/// list column currently hidden ("Size", "Category", "Tags", etc.; Name/
/// State/Progress are always shown). Null/empty means every column is
/// visible, matching the app's original fixed set - a flat string list
/// rather than one bool field per column, so a future column doesn't
/// need its own settings-schema change to be toggleable.
/// </param>
/// <param name="TorrentNotes">
/// Stage 6's per-torrent notes, keyed by info hash - purely local, never
/// sent to gottrentd or the Hub (there is no server-side concept of a
/// note). A torrent no longer in this dictionary simply has no note; an
/// empty note is never stored (see <c>MainViewModel.SetTorrentNote</c>),
/// so this only ever grows for torrents someone actually annotated.
/// </param>
/// <param name="HubBaseAddress">
/// Where to find an optional <c>GoTorrent.Hub</c> instance for Stage 6's
/// activity-history view - a completely separate, optional connection
/// from <see cref="BaseAddress"/>'s gottrentd one. Null means "no Hub
/// configured," the default - the feature this backs is opt-in.
/// </param>
/// <param name="HubToken">
/// The Hub's own JWT, obtained via <c>IHubClient.LoginAsync</c> and
/// encrypted at rest by <see cref="FileSettingsStore"/> exactly like
/// <see cref="Token"/> - never the username/password themselves, which
/// this app never persists.
/// </param>
public sealed record DesktopSettings(
    string? BaseAddress,
    string? Token,
    bool StartMinimized = false,
    double? WindowWidth = null,
    double? WindowHeight = null,
    int? WindowX = null,
    int? WindowY = null,
    bool WindowMaximized = false,
    double? DetailSplitFraction = null,
    bool LightTheme = false,
    bool CompactDensity = false,
    IReadOnlyList<string>? HiddenColumns = null,
    IReadOnlyList<string>? RecentDownloadDirs = null,
    IReadOnlyDictionary<string, string>? TorrentNotes = null,
    string? HubBaseAddress = null,
    string? HubToken = null)
{
    public bool IsConfigured => !string.IsNullOrWhiteSpace(BaseAddress) && !string.IsNullOrWhiteSpace(Token);

    public bool IsHubConfigured => !string.IsNullOrWhiteSpace(HubBaseAddress) && !string.IsNullOrWhiteSpace(HubToken);
}
