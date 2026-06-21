#ifndef MyAppVersion
#define MyAppVersion "dev"
#endif

#ifndef SourceDir
#define SourceDir "..\build\release\wintp-dev-windows-amd64-portable"
#endif

#ifndef OutputDir
#define OutputDir "..\dist"
#endif

#define MyAppName "wintp"
#define MyAppExeName "wintp.exe"

[Setup]
AppId={{7B6F33D8-9784-47DA-8E48-4B4F9D47C7A0}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher=wintp
DefaultDirName={autopf}\wintp
DefaultGroupName=wintp
DisableProgramGroupPage=yes
OutputDir={#OutputDir}
OutputBaseFilename=wintp-{#MyAppVersion}-setup
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
WizardStyle=modern
UninstallDisplayIcon={app}\{#MyAppExeName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts:"; Flags: unchecked

[Files]
Source: "{#SourceDir}\wintp.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\rclone.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\winfsp-2.1.25156.msi"; DestDir: "{tmp}"; Flags: deleteafterinstall
Source: "{#SourceDir}\README.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\wintp"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\wintp"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
Filename: "msiexec.exe"; Parameters: "/i ""{tmp}\winfsp-2.1.25156.msi"" /qn /norestart"; StatusMsg: "Installing WinFsp..."; Flags: waituntilterminated runhidden; Check: NeedsWinFsp
Filename: "{app}\{#MyAppExeName}"; Description: "Launch wintp"; Flags: nowait postinstall skipifsilent

[Code]
function NeedsWinFsp: Boolean;
begin
  Result :=
    not RegKeyExists(HKLM64, 'SOFTWARE\WinFsp') and
    not RegKeyExists(HKLM64, 'SOFTWARE\WOW6432Node\WinFsp') and
    not RegKeyExists(HKLM32, 'SOFTWARE\WinFsp') and
    not RegKeyExists(HKLM32, 'SOFTWARE\WOW6432Node\WinFsp');
end;
