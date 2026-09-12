using Microsoft.Win32;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// <see cref="IAutostartService"/> via the real per-user Windows "Run"
/// registry key — the same mechanism every ordinary autostart-with-Windows
/// app uses (no admin rights needed, unlike the machine-wide
/// <c>HKEY_LOCAL_MACHINE</c> equivalent, which this deliberately doesn't
/// touch). No-op (never touches the registry, always reports disabled) on
/// anything but Windows - matches the same "real OS-specific behavior
/// behind an explicit runtime guard" pattern the Go side's own
/// <c>shellcmd_windows.go</c>/<c>shellcmd_unix.go</c> split uses, since
/// there is no meaningful autostart mechanism to fall back to here.
/// </summary>
public sealed class WindowsAutostartService : IAutostartService
{
    private const string RunKeyPath = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string ValueName = "GoTorrent.Desktop";

    public bool IsEnabled()
    {
        if (!OperatingSystem.IsWindows())
        {
            return false;
        }
        using var key = Registry.CurrentUser.OpenSubKey(RunKeyPath, writable: false);
        return key?.GetValue(ValueName) as string == CommandLine();
    }

    public void SetEnabled(bool enabled)
    {
        if (!OperatingSystem.IsWindows())
        {
            return;
        }
        using var key = Registry.CurrentUser.OpenSubKey(RunKeyPath, writable: true)
            ?? Registry.CurrentUser.CreateSubKey(RunKeyPath);
        if (enabled)
        {
            key.SetValue(ValueName, CommandLine());
        }
        else
        {
            key.DeleteValue(ValueName, throwOnMissingValue: false);
        }
    }

    /// <summary>
    /// The exact command line written to the Run key - quoted so a path
    /// containing spaces (a real, not hypothetical, case: this repo's own
    /// dev machine has one in its username) still launches correctly.
    /// </summary>
    private static string CommandLine()
    {
        var path = Environment.ProcessPath ?? throw new InvalidOperationException("Could not determine this process's executable path.");
        return $"\"{path}\"";
    }
}
