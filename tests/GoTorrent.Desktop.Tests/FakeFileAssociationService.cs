using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>An in-memory <see cref="IFileAssociationService"/> - no real registry access in tests.</summary>
public sealed class FakeFileAssociationService : IFileAssociationService
{
    public bool Registered { get; set; }

    public bool IsRegistered() => Registered;

    public void SetRegistered(bool enabled) => Registered = enabled;
}
