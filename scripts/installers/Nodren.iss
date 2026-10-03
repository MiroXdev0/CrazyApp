#define ReleaseDir GetEnv("NODREN_INSTALLER_RELEASE_DIR")
#define OutputDir GetEnv("NODREN_INSTALLER_OUTPUT_DIR")
#define ProductVersion GetEnv("NODREN_INSTALLER_VERSION")

[Setup]
AppId={{6C2F776B-2C5E-4C7B-969D-22B5459FA101}
AppName=Nodren
AppVersion={#ProductVersion}
AppPublisher=Nodren
DefaultDirName={autopf}\Nodren
DefaultGroupName=Nodren
UninstallDisplayName=Nodren
UninstallDisplayIcon={app}\NodrenApp.exe
OutputDir={#OutputDir}
OutputBaseFilename=Install-Nodren
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog commandline
ChangesEnvironment=yes
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no
Uninstallable=yes
VersionInfoVersion={#ProductVersion}.0
VersionInfoProductVersion={#ProductVersion}
VersionInfoProductName=Nodren
VersionInfoDescription=Nodren desktop application and CLI installer

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional shortcuts:"; Flags: unchecked
Name: "addtopath"; Description: "Add the Nodren CLI directory to PATH"; GroupDescription: "Command-line access:"; Flags: checkedonce

[Files]
Source: "{#ReleaseDir}\NodrenApp.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#ReleaseDir}\nodren.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#ReleaseDir}\nodren-worker.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#ReleaseDir}\nodren-core.dll"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Nodren"; Filename: "{app}\NodrenApp.exe"; WorkingDir: "{app}"
Name: "{group}\Nodren CLI"; Filename: "{app}\nodren.exe"; WorkingDir: "{app}"
Name: "{group}\Nodren Worker help"; Filename: "{app}\nodren-worker.exe"; Parameters: "--help"; WorkingDir: "{app}"
Name: "{autodesktop}\Nodren"; Filename: "{app}\NodrenApp.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Code]
function EnvironmentRootKey: Integer;
begin
  if IsAdminInstallMode then
    Result := HKLM
  else
    Result := HKCU;
end;

function EnvironmentPathKey: string;
begin
  if IsAdminInstallMode then
    Result := 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment'
  else
    Result := 'Environment';
end;

function NodrenInstallKey: string;
begin
  Result := 'Software\Nodren\Installations\Desktop';
end;

function HasPathEntry(const PathValue, Directory: string): Boolean;
var
  Remaining, Entry: string;
  Separator: Integer;
begin
  Remaining := PathValue;
  while Remaining <> '' do
  begin
    Separator := Pos(';', Remaining);
    if Separator = 0 then
    begin
      Entry := Remaining;
      Remaining := '';
    end
    else
    begin
      Entry := Copy(Remaining, 1, Separator - 1);
      Delete(Remaining, 1, Separator);
    end;
    if CompareText(Trim(Entry), Directory) = 0 then
    begin
      Result := True;
      Exit;
    end;
  end;
  Result := False;
end;

procedure AddNodrenToPath;
var
  ExistingPath, NewPath: string;
  PreviouslyAdded: Cardinal;
begin
  if not WizardIsTaskSelected('addtopath') then
    Exit;
  if not RegQueryStringValue(EnvironmentRootKey, EnvironmentPathKey, 'Path', ExistingPath) then
    ExistingPath := '';
  if HasPathEntry(ExistingPath, ExpandConstant('{app}')) then
  begin
    if RegQueryDWordValue(EnvironmentRootKey, NodrenInstallKey, 'AddedPathEntry', PreviouslyAdded) and
       (PreviouslyAdded = 1) then
      Exit;
    RegWriteDWordValue(EnvironmentRootKey, NodrenInstallKey, 'AddedPathEntry', 0);
    Exit;
  end;
  if ExistingPath = '' then
    NewPath := ExpandConstant('{app}')
  else
    NewPath := ExistingPath + ';' + ExpandConstant('{app}');
  if not RegWriteExpandStringValue(EnvironmentRootKey, EnvironmentPathKey, 'Path', NewPath) then
    RaiseException('Could not add the Nodren CLI directory to PATH.');
  if not RegWriteDWordValue(EnvironmentRootKey, NodrenInstallKey, 'AddedPathEntry', 1) then
  begin
    if ExistingPath = '' then
      RegDeleteValue(EnvironmentRootKey, EnvironmentPathKey, 'Path')
    else
      RegWriteExpandStringValue(EnvironmentRootKey, EnvironmentPathKey, 'Path', ExistingPath);
    RaiseException('Could not record the Nodren PATH entry for safe uninstallation.');
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    AddNodrenToPath;
end;

procedure RemoveNodrenPathEntry;
var
  ExistingPath, Remaining, Entry, NewPath: string;
  Separator: Integer;
  AddedByNodren: Cardinal;
  FirstEntry: Boolean;
begin
  if not RegQueryDWordValue(EnvironmentRootKey, NodrenInstallKey, 'AddedPathEntry', AddedByNodren) or
     (AddedByNodren <> 1) then
    Exit;
  if not RegQueryStringValue(EnvironmentRootKey, EnvironmentPathKey, 'Path', ExistingPath) then
    Exit;

  Remaining := ExistingPath;
  NewPath := '';
  FirstEntry := True;
  repeat
  begin
    Separator := Pos(';', Remaining);
    if Separator = 0 then
    begin
      Entry := Remaining;
      Remaining := '';
    end
    else
    begin
      Entry := Copy(Remaining, 1, Separator - 1);
      Delete(Remaining, 1, Separator);
    end;
    if CompareText(Trim(Entry), ExpandConstant('{app}')) <> 0 then
    begin
      if not FirstEntry then
        NewPath := NewPath + ';';
      NewPath := NewPath + Entry;
      FirstEntry := False;
    end;
  end
  until Separator = 0;
  if NewPath <> ExistingPath then
  begin
    if not RegWriteExpandStringValue(EnvironmentRootKey, EnvironmentPathKey, 'Path', NewPath) then
      RaiseException('Could not safely remove the Nodren CLI directory from PATH.');
  end;
  RegDeleteValue(EnvironmentRootKey, NodrenInstallKey, 'AddedPathEntry');
  RegDeleteKeyIfEmpty(EnvironmentRootKey, NodrenInstallKey);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    RemoveNodrenPathEntry;
end;
