; XCut Windows setup — Inno Setup 6 script.
; Builds the true installer artifact (owner directive 2026-09-18):
; a single setup exe that installs xcut.exe with Start-menu / desktop
; shortcuts and a proper uninstaller. FFmpeg is deliberately NOT bundled —
; the app detects its absence on first run and offers a pinned, checksum-
; verified download into the install dir (internal/setup), keeping this
; installer small and the distribution license-clean.
;
; Build (from repo root, after build-release.ps1):
;   ISCC.exe /DAppVersion=0.1.7 installer/xcut.iss
; Output: dist/xcut-<AppVersion>-windows-setup.exe

#define AppName "XCut"
#define AppExe "xcut.exe"
#ifndef AppVersion
#define AppVersion "0.0.0-dev"
#endif

[Setup]
AppId={{B07B0886-97C4-4ECD-8D01-A3FD87ED4584}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher=XCut
DefaultDirName={autopf}\XCut
DefaultGroupName=XCut
DisableProgramGroupPage=yes
LicenseFile=..\LICENSE
OutputDir=..\dist
OutputBaseFilename=xcut-{#AppVersion}-windows-setup
SetupIconFile=..\dist\xcut.ico
UninstallDisplayIcon={app}\{#AppExe}
InfoBeforeFile=..\installer\SETUP-NOTES.txt
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesInstallIn64BitMode=x64compatible
ChangesEnvironment=no

[Files]
Source: "..\dist\xcut-{#AppVersion}-windows-amd64.exe"; DestDir: "{app}"; DestName: "{#AppExe}"; Flags: ignoreversion

[Icons]
Name: "{group}\XCut"; Filename: "{app}\{#AppExe}"
Name: "{group}\XCut (uninstall)"; Filename: "{uninstallexe}"
Name: "{autodesktop}\XCut"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Run]
Filename: "{app}\{#AppExe}"; Description: "{cm:LaunchProgram,{#AppName}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Workspace data lives in %USERPROFILE%\.xcut (user data, kept on uninstall);
; nothing under {app} outlives the install except possibly a downloaded
; ffmpeg in {app}\bin — remove that so uninstall is clean.
Type: filesandordirs; Name: "{app}\bin"
