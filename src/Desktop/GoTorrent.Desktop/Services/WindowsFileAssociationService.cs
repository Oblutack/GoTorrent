using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using Microsoft.Win32;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// <see cref="IFileAssociationService"/> via the real per-user
/// <c>HKCU\Software\Classes</c> registry hive - the same "no admin
/// rights needed" per-user scope <see cref="WindowsAutostartService"/>
/// uses, and (unlike the machine-wide <c>HKEY_CLASSES_ROOT</c>) the
/// correct place for one user's preference without affecting anyone
/// else who might use this machine. Registers both a <c>.torrent</c>
/// file association and a <c>magnet:</c> protocol handler, since a
/// user turning one on almost always wants the other too and gottrentd
/// treats them identically once added (see
/// <c>MainViewModel.AddFromArgumentAsync</c>). No-op on anything but
/// Windows, same guard as <see cref="WindowsAutostartService"/>.
///
/// <para>
/// A real dev machine this was tested against already had both a
/// <c>.torrent</c> handler (uTorrent) and a <c>magnet:</c> handler
/// (Stremio) installed - overwriting those and then simply deleting
/// them again on disable would have silently broken a real,
/// already-working association. So this backs up whatever default
/// value/icon/command was there before registering, under
/// <c>HKCU\Software\GoTorrent.Desktop\PreviousAssociations</c>, and
/// restores it (rather than just deleting) on disable. Deliberately
/// scoped to only what actually determines "which app opens this" -
/// <c>.torrent</c>'s secondary metadata (its <c>Content Type</c> value,
/// the <c>OpenWithProgids</c> subkey Explorer's "Open With" menu keeps)
/// is never touched in the first place, so there's nothing to restore
/// there either.
/// </para>
/// </summary>
public sealed class WindowsFileAssociationService : IFileAssociationService
{
    private const string TorrentProgId = "GoTorrent.Desktop.Torrent";
    private const string ClassesRoot = @"Software\Classes";
    private const string BackupKeyPath = @"Software\GoTorrent.Desktop\PreviousAssociations";

    /// <summary>A real command/icon/name is never empty, so this can't collide with one - marks "there was nothing here before" distinctly from "there was an empty string here".</summary>
    private const string NoPreviousValueMarker = "\0none";

    public bool IsRegistered()
    {
        if (!OperatingSystem.IsWindows())
        {
            return false;
        }
        using var command = Registry.CurrentUser.OpenSubKey($@"{ClassesRoot}\{TorrentProgId}\shell\open\command", writable: false);
        return command?.GetValue(null) as string == OpenCommand();
    }

    public void SetRegistered(bool enabled)
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }
        if (enabled)
        {
            Register();
        }
        else
        {
            Unregister();
        }
        // Explorer caches file associations - without this, a .torrent
        // file double-clicked right after registering can still launch
        // whatever the old default was until Explorer notices on its own.
        SHChangeNotify(ShcneAssocChanged, ShcnfIdList, IntPtr.Zero, IntPtr.Zero);
    }

    [SupportedOSPlatform("windows")]
    private static void Register()
    {
        BackUpDefaultValue(@".torrent");
        BackUpDefaultValue("magnet");
        BackUpDefaultValue(@"magnet\DefaultIcon");
        BackUpDefaultValue(@"magnet\shell\open\command");

        var openCommand = OpenCommand();
        var iconValue = $"{ExecutablePath()},0";

        using (var progIdKey = Registry.CurrentUser.CreateSubKey($@"{ClassesRoot}\{TorrentProgId}"))
        {
            progIdKey.SetValue(null, "GoTorrent Torrent File");
        }
        using (var iconKey = Registry.CurrentUser.CreateSubKey($@"{ClassesRoot}\{TorrentProgId}\DefaultIcon"))
        {
            iconKey.SetValue(null, iconValue);
        }
        using (var commandKey = Registry.CurrentUser.CreateSubKey($@"{ClassesRoot}\{TorrentProgId}\shell\open\command"))
        {
            commandKey.SetValue(null, openCommand);
        }
        using (var extKey = Registry.CurrentUser.CreateSubKey($@"{ClassesRoot}\.torrent"))
        {
            extKey.SetValue(null, TorrentProgId);
        }

        using (var protocolKey = Registry.CurrentUser.CreateSubKey($@"{ClassesRoot}\magnet"))
        {
            protocolKey.SetValue(null, "URL:Magnet Link Protocol");
            protocolKey.SetValue("URL Protocol", string.Empty);
        }
        using (var protocolIconKey = Registry.CurrentUser.CreateSubKey($@"{ClassesRoot}\magnet\DefaultIcon"))
        {
            protocolIconKey.SetValue(null, iconValue);
        }
        using (var protocolCommandKey = Registry.CurrentUser.CreateSubKey($@"{ClassesRoot}\magnet\shell\open\command"))
        {
            protocolCommandKey.SetValue(null, openCommand);
        }
    }

    [SupportedOSPlatform("windows")]
    private static void Unregister()
    {
        Registry.CurrentUser.DeleteSubKeyTree($@"{ClassesRoot}\{TorrentProgId}", throwOnMissingSubKey: false);

        RestoreDefaultValue(@".torrent");
        RestoreDefaultValue("magnet");
        RestoreDefaultValue(@"magnet\DefaultIcon");
        RestoreDefaultValue(@"magnet\shell\open\command");
    }

    /// <summary>Stashes <paramref name="relativePath"/>'s current default value (relative to <see cref="ClassesRoot"/>) for <see cref="RestoreDefaultValue"/> to bring back later.</summary>
    [SupportedOSPlatform("windows")]
    private static void BackUpDefaultValue(string relativePath)
    {
        using var key = Registry.CurrentUser.OpenSubKey($@"{ClassesRoot}\{relativePath}", writable: false);
        var existing = key?.GetValue(null) as string;
        using var backupKey = Registry.CurrentUser.CreateSubKey(BackupKeyPath);
        backupKey.SetValue(BackupValueName(relativePath), existing ?? NoPreviousValueMarker);
    }

    /// <summary>
    /// Undoes <see cref="BackUpDefaultValue"/> - restores the previous
    /// value if there was one, otherwise removes what this app itself
    /// added. <paramref name="relativePath"/>'s own key is never deleted
    /// outright (only its default value, or - for the two leaf paths
    /// under <c>magnet\</c>, which this app owns entirely once it has
    /// created them - the whole subtree) so a sibling value or subkey
    /// this app never touched (<c>.torrent</c>'s <c>Content Type</c>,
    /// say) is never at risk.
    /// </summary>
    [SupportedOSPlatform("windows")]
    private static void RestoreDefaultValue(string relativePath)
    {
        var fullPath = $@"{ClassesRoot}\{relativePath}";
        using var backupKey = Registry.CurrentUser.OpenSubKey(BackupKeyPath, writable: true);
        var backedUp = backupKey?.GetValue(BackupValueName(relativePath)) as string;

        if (backedUp is null or NoPreviousValueMarker)
        {
            if (relativePath is @"magnet\DefaultIcon" or @"magnet\shell\open\command")
            {
                Registry.CurrentUser.DeleteSubKeyTree(fullPath, throwOnMissingSubKey: false);
            }
            else
            {
                using var key = Registry.CurrentUser.OpenSubKey(fullPath, writable: true);
                key?.DeleteValue(string.Empty, throwOnMissingValue: false);
            }
        }
        else
        {
            using var key = Registry.CurrentUser.CreateSubKey(fullPath);
            key.SetValue(null, backedUp);
        }

        backupKey?.DeleteValue(BackupValueName(relativePath), throwOnMissingValue: false);
    }

    private static string BackupValueName(string relativePath) => relativePath.Replace('\\', '_');

    private static string ExecutablePath() =>
        Environment.ProcessPath ?? throw new InvalidOperationException("Could not determine this process's executable path.");

    /// <summary>
    /// <c>%1</c> is the file path or <c>magnet:</c> URI the shell
    /// substitutes in - <see cref="App"/> reads it back off
    /// <c>IClassicDesktopStyleApplicationLifetime.Args</c> at startup.
    /// </summary>
    private static string OpenCommand() => $"\"{ExecutablePath()}\" \"%1\"";

    [DllImport("shell32.dll")]
    private static extern void SHChangeNotify(int wEventId, int uFlags, IntPtr dwItem1, IntPtr dwItem2);

    private const int ShcneAssocChanged = 0x08000000;
    private const int ShcnfIdList = 0x0000;
}
