namespace GoTorrent.Desktop.Services;

/// <summary>
/// Checks whether a newer release exists - the seam
/// <see cref="ViewModels.MainViewModel.CheckForUpdatesAsync"/> uses so
/// tests can fake "a newer version exists"/"none does"/"the check
/// failed" without a real network call, the same seam-per-concern
/// discipline as <see cref="IAutostartService"/>/<see cref="IDaemonLauncher"/>.
/// </summary>
public interface IUpdateChecker
{
    /// <summary>
    /// Returns the latest release's tag (e.g. <c>"v0.2.0"</c>), or null if
    /// the check failed for any reason (offline, GitHub unreachable, an
    /// unexpected response shape) - this is advisory only, so a failure
    /// is never an error to surface, just "nothing to report."
    /// </summary>
    Task<string?> GetLatestVersionTagAsync(CancellationToken cancellationToken);
}
