using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>An in-memory <see cref="IAutostartService"/> - no real registry access in tests.</summary>
public sealed class FakeAutostartService : IAutostartService
{
    public bool Enabled { get; set; }

    public bool IsEnabled() => Enabled;

    public void SetEnabled(bool enabled) => Enabled = enabled;
}
