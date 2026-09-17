; GoTorrent Windows installer (Inno Setup 6).
;
; Built by installer\build.ps1, which runs the real go build / dotnet
; publish steps first - this script only ever packages files that
; already exist under installer\build\, it never builds anything
; itself. Bundles gottrentd.exe (the headless daemon the desktop app
; talks to) and gottrent.exe (the CLI, a natural companion for anyone
; who'd rather script it) alongside the desktop app itself, per
; ROADMAP.md's own 6.4 wording ("bundling gottrentd.exe + the desktop
; app").
;
; Deliberately unsigned - see installer\README.md for why "signed
; builds" is a real, documented gap rather than a self-signed
; certificate pretending to be a real one.

#ifndef MyAppVersion
  #define MyAppVersion "0.1.0"
#endif
#define MyAppName "GoTorrent"
#define MyAppPublisher "Oblutack"
#define MyAppURL "https://github.com/Oblutack/GoTorrent"
#define MyAppExeName "GoTorrent.Desktop.exe"

[Setup]
AppId={{6F2E9B1A-6C0B-4E2E-9C7D-4E2C1B6E9B1A}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}/releases
; Per-user install (no admin rights needed) - matches every other
; Windows-integration feature this app already uses (autostart,
; file/protocol association all live under HKCU, never HKLM).
DefaultDirName={autopf}\GoTorrent
PrivilegesRequired=lowest
DefaultGroupName=GoTorrent
DisableProgramGroupPage=yes
OutputDir=Output
OutputBaseFilename=GoTorrentSetup
SetupIconFile=..\src\Desktop\GoTorrent.Desktop\Assets\app-icon.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
LicenseFile=..\LICENSE
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayIcon={app}\{#MyAppExeName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Additional shortcuts:"

[Files]
Source: "build\desktop\{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "build\gottrentd.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "build\gottrent.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "Launch {#MyAppName}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{app}"

; Known, honestly-documented gap: if autostart or .torrent/magnet:
; association is still enabled at uninstall time, this installer does
; not turn them off first - it only deletes {app}. The app's own
; restore logic (WindowsFileAssociationService's backup/restore design)
; only runs when a user unchecks those settings from within a *running*
; app, never automatically on uninstall. A user who uninstalls with
; either still enabled is left with a stale HKCU\...\Run entry or a
; stale file/protocol association pointing at a path that no longer
; exists. Fixing this properly needs a silent "unregister everything"
; CLI path in the app itself for the uninstaller to call - left for a
; follow-up, not solved here.
