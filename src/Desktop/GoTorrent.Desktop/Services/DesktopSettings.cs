namespace GoTorrent.Desktop.Services;

/// <summary>
/// Which gottrentd to connect to, plus app-behavior preferences,
/// remembered between runs. See <see cref="ISettingsStore"/> for where
/// this is persisted. <see cref="StartMinimized"/> lives here rather
/// than its own file - it's the only local-preference persistence this
/// app has, and a second settings file for one bool isn't worth the
/// extra seam.
/// </summary>
public sealed record DesktopSettings(string? BaseAddress, string? Token, bool StartMinimized = false)
{
    public bool IsConfigured => !string.IsNullOrWhiteSpace(BaseAddress) && !string.IsNullOrWhiteSpace(Token);
}
