<#
.SYNOPSIS
    Builds every piece 6.4's installer bundles, then compiles the
    installer itself - the one script a fresh clone actually needs to
    run to produce installer\Output\GoTorrentSetup.exe.

.DESCRIPTION
    Three real build steps, kept as one script rather than three
    documented commands so "how do I build the installer" has exactly
    one answer:
      1. go build gottrent.exe / gottrentd.exe for windows/amd64 (native
         on this dev machine - no cross-compile flags needed here, but
         GOOS/GOARCH are set explicitly anyway so this also works from a
         non-Windows shell running under WSL/Git Bash).
      2. dotnet publish the Desktop app as a self-contained, single-file
         win-x64 executable - no .NET runtime needs to be installed on
         the machine that runs the installer.
      3. ISCC.exe (Inno Setup's command-line compiler) compiles
         installer\gotorrent.iss against the two steps above.

.NOTES
    Deliberately does not sign anything - see installer\README.md for
    why "signed builds" is a real, honestly-documented gap rather than
    a self-signed certificate pretending to be one.
#>
[CmdletBinding()]
param(
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$buildDir = Join-Path $PSScriptRoot "build"

Write-Host "==> Building gottrent.exe / gottrentd.exe (windows/amd64)"
New-Item -ItemType Directory -Force -Path $buildDir | Out-Null
$env:GOOS = "windows"
$env:GOARCH = "amd64"
& go build -o (Join-Path $buildDir "gottrent.exe") "$repoRoot/cmd/gottrent"
if ($LASTEXITCODE -ne 0) { throw "go build gottrent failed" }
& go build -o (Join-Path $buildDir "gottrentd.exe") "$repoRoot/cmd/gottrentd"
if ($LASTEXITCODE -ne 0) { throw "go build gottrentd failed" }
Remove-Item Env:\GOOS, Env:\GOARCH -ErrorAction SilentlyContinue

Write-Host "==> Publishing GoTorrent.Desktop (self-contained, single-file, win-x64)"
$desktopProject = Join-Path $repoRoot "src\Desktop\GoTorrent.Desktop\GoTorrent.Desktop.csproj"
$desktopOut = Join-Path $buildDir "desktop"
& dotnet publish $desktopProject -c Release -r win-x64 --self-contained true `
    -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true `
    -p:Version=$Version -o $desktopOut
if ($LASTEXITCODE -ne 0) { throw "dotnet publish failed" }

Write-Host "==> Compiling the installer"
$iscc = "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
if (-not (Test-Path $iscc)) {
    $iscc = "ISCC.exe" # fall back to PATH, e.g. a machine-wide install
}
& $iscc "/DMyAppVersion=$Version" (Join-Path $PSScriptRoot "gotorrent.iss")
if ($LASTEXITCODE -ne 0) { throw "ISCC.exe failed" }

Write-Host "==> Done: installer\Output\GoTorrentSetup.exe"
