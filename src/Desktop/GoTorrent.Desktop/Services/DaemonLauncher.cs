using System.ComponentModel;
using System.Diagnostics;

namespace GoTorrent.Desktop.Services;

/// <summary>
/// <see cref="IDaemonLauncher"/> via a real <see cref="Process"/> - no
/// Windows-specific API involved (<see cref="Process.Start(ProcessStartInfo)"/>
/// works the same on every OS this project's plain <c>net10.0</c> TFM
/// targets), unlike <see cref="WindowsAutostartService"/>/
/// <see cref="WindowsFileAssociationService"/>, which is why this class
/// isn't named <c>Windows...</c> the way those are.
///
/// <para>
/// The executable is looked for only right next to this app's own binary
/// (<see cref="AppContext.BaseDirectory"/>) - the layout ROADMAP.md's 6.4
/// packaging plan already commits to ("an installer bundling
/// <c>gottrentd.exe</c> + the desktop app"). No PATH search, no
/// repo-relative dev-machine guessing: those would only ever be right on
/// this one dev machine, not a real install.
/// </para>
///
/// <para>
/// A spawned gottrentd is deliberately given no <c>-config</c>/<c>-state-dir</c>
/// override, only <c>-api-address</c> - so its token file lands at the
/// exact same default path (<c>os.UserConfigDir()/GoTorrent/api-token</c>
/// on the Go side, <c>%AppData%/GoTorrent/api-token</c> here) a bare
/// <c>gottrentd</c> invocation would use, letting <see cref="TryReadExistingToken"/>
/// find it with no coordination needed beyond agreeing on that one path.
/// <see cref="ProcessStartInfo.WorkingDirectory"/> is set to a real
/// downloads folder rather than passing <c>-dir</c> explicitly, so
/// gottrentd's own <c>DownloadDir</c> default (<c>"."</c>) resolves
/// somewhere sensible instead of wherever this app happens to be
/// installed.
/// </para>
/// </summary>
public sealed class DaemonLauncher : IDaemonLauncher
{
    private static readonly string TokenPath = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "GoTorrent", "api-token");

    private static readonly TimeSpan StartupTimeout = TimeSpan.FromSeconds(10);

    private Process? _process;

    public bool IsAvailable => TryFindExecutable(out _);

    public bool IsRunning => _process is { HasExited: false };

    public string? TryReadExistingToken()
    {
        try
        {
            return File.Exists(TokenPath) ? File.ReadAllText(TokenPath).Trim() : null;
        }
        catch (IOException)
        {
            return null;
        }
    }

    public async Task<string?> StartAsync(string apiAddress, CancellationToken cancellationToken)
    {
        if (!TryFindExecutable(out var executablePath))
        {
            return null;
        }

        // "GoTorrent Downloads", not a bare "GoTorrent" - a real dev
        // machine this was tested against already had an unrelated file
        // (not a directory) sitting at Downloads\GoTorrent, and a plain
        // app-name folder is a plausible enough collision (a downloaded
        // release archive, an extracted build) that a more specific name
        // is worth the extra few characters even with the try/catch above
        // already making the collision non-fatal.
        var downloadsDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), "Downloads", "GoTorrent Downloads");
        try
        {
            // A real dev machine hit this: an unrelated pre-existing file
            // (not a directory) happened to sit at exactly this path,
            // which CreateDirectory treats as a hard failure rather than
            // a no-op - not something to ever crash the whole app over,
            // the same "system boundary, expect it to fail" reasoning as
            // every other real I/O in this codebase.
            Directory.CreateDirectory(downloadsDir);

            var startInfo = new ProcessStartInfo
            {
                FileName = executablePath,
                WorkingDirectory = downloadsDir,
                UseShellExecute = false,
                CreateNoWindow = true,
            };
            startInfo.ArgumentList.Add("-api-address");
            startInfo.ArgumentList.Add(apiAddress);

            _process = Process.Start(startInfo);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException or InvalidOperationException or Win32Exception)
        {
            return null;
        }
        if (_process is null)
        {
            return null;
        }

        var deadline = DateTime.UtcNow + StartupTimeout;
        while (DateTime.UtcNow < deadline)
        {
            if (_process.HasExited)
            {
                // Most likely something is already bound to apiAddress -
                // gottrentd treats that bind as its single-instance lock
                // and refuses to start rather than silently picking
                // another port.
                return null;
            }
            if (File.Exists(TokenPath))
            {
                try
                {
                    var token = await File.ReadAllTextAsync(TokenPath, cancellationToken);
                    return token.Trim();
                }
                catch (IOException)
                {
                    // Caught mid-write; retry on the next poll.
                }
            }
            await Task.Delay(TimeSpan.FromMilliseconds(250), cancellationToken);
        }
        return null;
    }

    public void Stop()
    {
        if (_process is { HasExited: false } process)
        {
            try
            {
                process.Kill();
            }
            catch (InvalidOperationException)
            {
                // Already exited between the HasExited check and Kill().
            }
        }
    }

    private static bool TryFindExecutable(out string executablePath)
    {
        var executableName = OperatingSystem.IsWindows() ? "gottrentd.exe" : "gottrentd";
        var candidate = Path.Combine(AppContext.BaseDirectory, executableName);
        if (File.Exists(candidate))
        {
            executablePath = candidate;
            return true;
        }
        executablePath = string.Empty;
        return false;
    }
}
