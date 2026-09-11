namespace GoTorrent.Desktop.Services;

/// <summary>Which gottrentd to connect to, remembered between runs. See <see cref="ISettingsStore"/> for where this is persisted.</summary>
public sealed record DesktopSettings(string? BaseAddress, string? Token)
{
    public bool IsConfigured => !string.IsNullOrWhiteSpace(BaseAddress) && !string.IsNullOrWhiteSpace(Token);
}
