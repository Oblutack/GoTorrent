using GoTorrent.Desktop.Services;

namespace GoTorrent.Desktop.Tests;

/// <summary>
/// Real file I/O against a real temp directory (via the path-overload
/// constructor 6.5 added), never the real user profile - same "don't make
/// every test write into the developer's real %AppData%" reasoning that
/// made <see cref="ISettingsStore"/> its own seam in the first place.
/// </summary>
public sealed class FileSettingsStoreTests : IDisposable
{
    private readonly string _dir;
    private readonly string _filePath;

    public FileSettingsStoreTests()
    {
        _dir = Path.Combine(Path.GetTempPath(), "GoTorrentDesktopTests-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(_dir);
        _filePath = Path.Combine(_dir, "settings.json");
    }

    public void Dispose() => Directory.Delete(_dir, recursive: true);

    [Fact]
    public void Load_WithNoFile_ReturnsDefaults()
    {
        var store = new FileSettingsStore(_filePath);
        var settings = store.Load();
        Assert.False(settings.IsConfigured);
    }

    [Fact]
    public void SaveThenLoad_RoundTrips()
    {
        var store = new FileSettingsStore(_filePath);
        store.Save(new DesktopSettings("http://127.0.0.1:6880/", "a-token", StartMinimized: true));

        var loaded = store.Load();

        Assert.Equal("http://127.0.0.1:6880/", loaded.BaseAddress);
        Assert.Equal("a-token", loaded.Token);
        Assert.True(loaded.StartMinimized);
    }

    [Fact]
    public void Load_WithCorruptFile_ReturnsDefaultsInsteadOfThrowing()
    {
        Directory.CreateDirectory(_dir);
        File.WriteAllText(_filePath, "{ this is not valid json");
        var store = new FileSettingsStore(_filePath);

        var settings = store.Load();

        Assert.False(settings.IsConfigured);
    }

    [Fact]
    public void Load_WithCorruptFile_QuarantinesRatherThanDeletesIt()
    {
        Directory.CreateDirectory(_dir);
        File.WriteAllText(_filePath, "{ this is not valid json");
        var store = new FileSettingsStore(_filePath);

        store.Load();

        Assert.False(File.Exists(_filePath));
        var quarantined = Directory.GetFiles(_dir, "settings.json.corrupt-*");
        Assert.Single(quarantined);
        Assert.Contains("not valid json", File.ReadAllText(quarantined[0]));
    }

    [Fact]
    public void Save_DoesNotLeaveATempFileBehind()
    {
        var store = new FileSettingsStore(_filePath);
        store.Save(new DesktopSettings("http://127.0.0.1:6880/", "a-token"));

        var leftovers = Directory.GetFiles(_dir, "settings.json.tmp-*");
        Assert.Empty(leftovers);
    }
}
