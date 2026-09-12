using System.Text.Json;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// Persists <see cref="DesktopSettings"/> as a small JSON file under the
/// OS's per-user app-data directory - the same "config lives outside the
/// install/download directory" idea gottrentd's own resume data and
/// manifest already use on the Go side.
/// </summary>
public sealed class FileSettingsStore : ISettingsStore
{
    private static readonly string DefaultFilePath = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "GoTorrent.Desktop", "settings.json");

    private readonly string _filePath;

    /// <summary>Persists under the real per-user app-data directory - the only constructor <see cref="ViewModels.MainViewModel"/> itself ever uses.</summary>
    public FileSettingsStore() : this(DefaultFilePath)
    {
    }

    /// <summary>
    /// Lets tests point this at a real temp-directory file instead of the
    /// real user profile - same "don't make every test write into the
    /// developer's real %AppData%" reasoning that made <see cref="ISettingsStore"/>
    /// its own seam in the first place, just one level deeper for the one
    /// implementation that actually needs to prove its file-I/O works.
    /// </summary>
    public FileSettingsStore(string filePath)
    {
        _filePath = filePath;
    }

    /// <summary>
    /// A corrupt or unreadable settings file used to be an unhandled
    /// exception straight out of the <see cref="ViewModels.MainViewModel"/>
    /// constructor - no window, no message, nothing a user could act on
    /// without knowing to go delete a file they've never heard of. Falls
    /// back to defaults instead, same as "no settings file yet." The bad
    /// file is renamed aside (<c>settings.json.corrupt-&lt;ticks&gt;</c>),
    /// not deleted outright - never destroy something unexpected found on
    /// disk without a way back, same discipline 6.3's file-association
    /// backup/restore work already established for this app.
    /// </summary>
    public DesktopSettings Load()
    {
        if (!File.Exists(_filePath))
        {
            return new DesktopSettings(null, null);
        }
        try
        {
            var json = File.ReadAllText(_filePath);
            return JsonSerializer.Deserialize<DesktopSettings>(json) ?? new DesktopSettings(null, null);
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            TryQuarantine();
            return new DesktopSettings(null, null);
        }
    }

    private void TryQuarantine()
    {
        try
        {
            var quarantinePath = $"{_filePath}.corrupt-{DateTimeOffset.UtcNow.Ticks}";
            File.Move(_filePath, quarantinePath, overwrite: true);
        }
        catch
        {
            // Best-effort only - if even renaming it aside fails (e.g. the
            // file is locked by something else), falling back to defaults
            // above still lets the app start rather than crash.
        }
    }

    /// <summary>
    /// Writes via a temp file + atomic rename, the same pattern gottrentd's
    /// own resume data and fleet manifest already use on the Go side - a
    /// crash or power loss mid-write leaves the old file (or nothing)
    /// intact rather than a half-written file <see cref="Load"/> would
    /// then have to quarantine on the next launch.
    /// </summary>
    public void Save(DesktopSettings settings)
    {
        var directory = Path.GetDirectoryName(_filePath)!;
        Directory.CreateDirectory(directory);
        var tempPath = Path.Combine(directory, $"settings.json.tmp-{Guid.NewGuid():N}");
        File.WriteAllText(tempPath, JsonSerializer.Serialize(settings));
        File.Move(tempPath, _filePath, overwrite: true);
    }
}
