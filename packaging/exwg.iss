#define AppName "Ex WG"
; The version comes from /VERSION via build-flutter-windows.ps1 (/DAppVersion=x.y.z).
#ifndef AppVersion
  #error AppVersion is not defined. Build with build-flutter-windows.ps1.
#endif
#define AppNumericVersion AppVersion + ".0"
#define AppUserModelId "ExWG.Client.Windows"
#define AppPublisher "Ex WG"
#define AppExeName "ExWG.exe"

[Setup]
AppId={{8B54C2E9-3F17-4A92-B6D1-7E4C9A2F8015}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
VersionInfoVersion={#AppNumericVersion}
VersionInfoCompany={#AppPublisher}
VersionInfoDescription={#AppName} installer
VersionInfoProductName={#AppName}
VersionInfoProductVersion={#AppNumericVersion}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\dist
OutputBaseFilename=Ex-WG-Setup-{#AppVersion}
SetupIconFile=..\cmd\exwg_flutter\windows\runner\resources\app_icon.ico
UninstallDisplayIcon={app}\{#AppExeName}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no
ChangesEnvironment=no
ChangesAssociations=no
MinVersion=10.0.17763

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Создать ярлык на рабочем столе"; GroupDescription: "Дополнительные ярлыки:"; Flags: unchecked

[Files]
Source: "..\cmd\exwg_flutter\build\windows\x64\runner\Release\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppExeName}"; WorkingDir: "{app}"; AppUserModelID: "{#AppUserModelId}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; WorkingDir: "{app}"; AppUserModelID: "{#AppUserModelId}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExeName}"; Description: "Запустить {#AppName}"; Flags: nowait postinstall runascurrentuser

[UninstallRun]
Filename: "{app}\ExWGCore.exe"; Parameters: "--cleanup"; Flags: runhidden waituntilterminated; RunOnceId: "CleanupExWGTunnel"

[Code]
const
  LegacyUninstallKey = 'SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{AFA09D48-C6E4-4A86-946C-4C81B9C667C2}_is1';

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  CoreExe: String;
  LegacyDir: String;
  LegacyUninstaller: String;
begin
  Result := '';
  CoreExe := ExpandConstant('{app}\ExWGCore.exe');
  // 0.9.0+ cores close the UI and remove the tunnel gracefully.
  if FileExists(CoreExe) then
  begin
    Exec(CoreExe, '--cleanup', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
  // Stop the tunnel service and running processes before replacing or migrating files.
  Exec(ExpandConstant('{sys}\sc.exe'), 'stop "NexusAWGTunnel$Nexus"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Sleep(1500);
  Exec(ExpandConstant('{sys}\sc.exe'), 'delete "NexusAWGTunnel$Nexus"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/IM ExWG.exe /F /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/IM ExWGCore.exe /F /T', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  // If a pre-1.0 installation is registered under the old AppId, uninstall it
  // silently and remove its folder so Ex WG installs cleanly into {autopf}\Ex WG.
  LegacyDir := '';
  RegQueryStringValue(HKLM, LegacyUninstallKey, 'Inno Setup: App Path', LegacyDir);
  if RegQueryStringValue(HKLM, LegacyUninstallKey, 'UninstallString', LegacyUninstaller) and (LegacyUninstaller <> '') then
  begin
    LegacyUninstaller := RemoveQuotes(LegacyUninstaller);
    if FileExists(LegacyUninstaller) then
    begin
      Exec(LegacyUninstaller, '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    end;
  end;
  if (LegacyDir <> '') and DirExists(LegacyDir) and (CompareText(RemoveBackslashUnlessRoot(LegacyDir), RemoveBackslashUnlessRoot(ExpandConstant('{app}'))) <> 0) then
  begin
    DelTree(LegacyDir, True, True, True);
  end;
end;
