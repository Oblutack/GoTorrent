using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>An in-memory <see cref="ISettingsStore"/> - no real file I/O in tests.</summary>
public sealed class FakeSettingsStore : ISettingsStore
{
    private DesktopSettings _settings = new(null, null);

    public DesktopSettings Load() => _settings;

    public void Save(DesktopSettings settings) => _settings = settings;
}
