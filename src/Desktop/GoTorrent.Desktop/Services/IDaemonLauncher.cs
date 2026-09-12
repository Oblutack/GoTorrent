namespace GoTorrent.Desktop.Services;

/// <summary>
/// Locates and, if needed, spawns a local <c>gottrentd</c> process - the
/// "spawn if not [running]" half of 6.3's daemon supervision. "Attach if
/// running" needs no code here at all: it's just <see cref="ViewModels.MainViewModel"/>
/// trying a real API call against whatever <see cref="TryReadExistingToken"/>
/// returns before ever calling <see cref="StartAsync"/>.
/// </summary>
public interface IDaemonLauncher
{
    /// <summary>True if a local <c>gottrentd</c> executable was found next to this app.</summary>
    bool IsAvailable { get; }

    /// <summary>True if this instance spawned a daemon process that is still running.</summary>
    bool IsRunning { get; }

    /// <summary>
    /// Reads gottrentd's own default bearer token file, if one exists -
    /// evidence a gottrentd has run here before (and, combined with a real
    /// reachability check the caller makes itself, whether one still is).
    /// Never spawns anything; a missing file just means null.
    /// </summary>
    string? TryReadExistingToken();

    /// <summary>
    /// Spawns gottrentd bound to <paramref name="apiAddress"/> and waits for
    /// its bearer token to become available, returning it - or null if the
    /// executable couldn't be found, the process exited immediately (most
    /// likely because something is already bound to that address), or no
    /// token appeared within a reasonable startup window.
    /// </summary>
    Task<string?> StartAsync(string apiAddress, CancellationToken cancellationToken);

    /// <summary>Stops the daemon process this instance spawned, if any and if it's still running.</summary>
    void Stop();
}
