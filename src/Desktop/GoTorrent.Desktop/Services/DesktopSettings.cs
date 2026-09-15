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
    IReadOnlyList<string>? RecentDownloadDirs = null)
{
    public bool IsConfigured => !string.IsNullOrWhiteSpace(BaseAddress) && !string.IsNullOrWhiteSpace(Token);
}
