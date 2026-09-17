namespace GoTorrent.Desktop;

/// <summary>
/// This build's version - bumped by hand alongside this project's own
/// <c>&lt;Version&gt;</c> in <c>GoTorrent.Desktop.csproj</c> and the Go
/// side's <c>internal/version.String</c>, the same "no build-time
/// injection yet, real release-pipeline work" precedent <c>version.go</c>
/// itself already documents. Kept as a plain string constant rather than
/// read via reflection off the running assembly, so
/// <see cref="Services.GitHubUpdateChecker"/>'s version comparison has a
/// value to compare against even in a unit test with no real assembly
/// metadata to read.
/// </summary>
public static class AppVersion
{
    public const string Current = "0.1.0";
}
