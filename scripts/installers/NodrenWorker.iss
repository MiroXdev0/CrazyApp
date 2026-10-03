#define ReleaseDir GetEnv("NODREN_INSTALLER_RELEASE_DIR")
#define OutputDir GetEnv("NODREN_INSTALLER_OUTPUT_DIR")
#define ProductVersion GetEnv("NODREN_INSTALLER_VERSION")

[Setup]
AppId={{6C2F776B-2C5E-4C7B-969D-22B5459FA102}
AppName=Nodren Worker
AppVersion={#ProductVersion}
AppPublisher=Nodren
DefaultDirName={autopf}\Nodren\Worker
DefaultGroupName=Nodren Worker
UninstallDisplayName=Nodren Worker
UninstallDisplayIcon={app}\nodren-worker.exe
OutputDir={#OutputDir}
OutputBaseFilename=Install-Nodren-Worker
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog commandline
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no
Uninstallable=yes
VersionInfoVersion={#ProductVersion}.0
VersionInfoProductVersion={#ProductVersion}
VersionInfoProductName=Nodren Worker
VersionInfoDescription=Nodren Worker installer

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "{#ReleaseDir}\nodren-worker.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#ReleaseDir}\nodren-core.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "Worker-README.txt"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Nodren Worker configuration"; Filename: "{app}\Worker-README.txt"
Name: "{group}\Nodren Worker help"; Filename: "{app}\nodren-worker.exe"; Parameters: "--help"; WorkingDir: "{app}"
Name: "{group}\Uninstall Nodren Worker"; Filename: "{uninstallexe}"
