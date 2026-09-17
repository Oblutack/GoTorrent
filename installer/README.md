# GoTorrent Windows installer

Phase 6.4 of `ROADMAP.md`. Produces a single `GoTorrentSetup.exe` that
installs the desktop app plus `gottrentd.exe`/`gottrent.exe` (the daemon
and CLI), per-user (no admin rights needed - the same `HKCU`-only scope
this app's own autostart/file-association features already use).

## Building it

Prerequisites: Go 1.24+, the .NET 10 SDK, and
[Inno Setup 6](https://jrsoftware.org/isinfo.php) (`winget install
JRSoftware.InnoSetup` on Windows).

```powershell
installer\build.ps1
```

This builds `gottrent.exe`/`gottrentd.exe` (native `go build`, windows/amd64),
publishes the desktop app as a self-contained single-file `win-x64`
executable (`dotnet publish`, no .NET runtime needs to be installed on
the machine that runs the installer), then compiles
`installer\gotorrent.iss` into `installer\Output\GoTorrentSetup.exe`.

## What this does *not* do, honestly

- **Signed builds.** `GoTorrentSetup.exe` and the binaries inside it are
  unsigned - Windows SmartScreen will warn on first run. Real code
  signing needs a certificate from a CA (a paid product, or a personal
  one issued to a real identity) that this project doesn't have. A
  self-signed certificate was deliberately *not* used as a stand-in -
  it would look signed without actually being trusted by anything,
  which is worse than being honestly unsigned. Whoever owns a real
  certificate can sign `installer\Output\GoTorrentSetup.exe` (and
  ideally the three `.exe`s inside `installer\build\`) after this
  script runs; this script doesn't attempt to.
- **Auto-update installation.** The desktop app checks GitHub's
  Releases API on startup and offers a "View" link to the latest
  release (`Services/GitHubUpdateChecker.cs`) - it does not download
  or install an update itself. Silent/automatic update installation is
  real scope beyond a version check and is not attempted here.
- **Unregistering autostart/file-association on uninstall.** See the
  `[UninstallDelete]` section's own comment in `gotorrent.iss` - a
  known, deliberate gap, not silently dropped.
- **`goreleaser` does not build this installer.** `.goreleaser.yml` (repo
  root) only builds the cross-platform Go binaries and uploads them to
  a GitHub release; this Windows-only installer is a separate artifact,
  built by this script and uploaded to the same release by hand (or by
  a future CI job - not wired up yet).
