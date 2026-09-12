using GoTorrent.Desktop.Models;

namespace GoTorrent.Desktop.Tests;

/// <summary>
/// Regression coverage for a real gotcha caught live while building 6.5's
/// sidebar counts: a C# record's compiler-synthesized equality includes
/// every public property, not just the primary constructor's positional
/// ones - <see cref="SidebarFilter.Count"/> was silently part of it until
/// <see cref="SidebarFilter"/> got its own manual <c>Equals</c>/
/// <c>GetHashCode</c> override. Without that override, these two tests
/// would fail the moment <c>Count</c> differs, which is exactly what broke
/// <c>MainViewModel</c>'s <c>SyncCollection</c> reference-preservation and
/// its "is the live SelectedFilter still real" check.
/// </summary>
public sealed class SidebarFilterTests
{
    [Fact]
    public void Equals_IgnoresCount()
    {
        var a = new SidebarFilter(SidebarFilter.SeedingKey, "Seeding") { Count = 1 };
        var b = new SidebarFilter(SidebarFilter.SeedingKey, "Seeding") { Count = 2 };

        Assert.Equal(a, b);
        Assert.Equal(a.GetHashCode(), b.GetHashCode());
    }

    [Fact]
    public void Equals_StillDistinguishesByKey()
    {
        var seeding = new SidebarFilter(SidebarFilter.SeedingKey, "Seeding");
        var paused = new SidebarFilter(SidebarFilter.PausedKey, "Paused");

        Assert.NotEqual(seeding, paused);
    }
}
