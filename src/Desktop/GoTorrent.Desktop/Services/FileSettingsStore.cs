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
    private static readonly string FilePath = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "GoTorrent.Desktop", "settings.json");

    public DesktopSettings Load()
    {
        if (!File.Exists(FilePath))
        {
            return new DesktopSettings(null, null);
        }
        var json = File.ReadAllText(FilePath);
        return JsonSerializer.Deserialize<DesktopSettings>(json) ?? new DesktopSettings(null, null);
    }

    public void Save(DesktopSettings settings)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(FilePath)!);
        File.WriteAllText(FilePath, JsonSerializer.Serialize(settings));
    }
}
