namespace GoTorrent.Desktop.Services;

/// <summary>
/// Reads/writes <see cref="DesktopSettings"/> - its own seam (rather
/// than <see cref="DesktopSettings"/> doing file I/O on itself) so
/// <see cref="ViewModels.MainViewModel"/> is testable without touching
/// the real file system.
/// </summary>
public interface ISettingsStore
{
    DesktopSettings Load();

    void Save(DesktopSettings settings);
}
