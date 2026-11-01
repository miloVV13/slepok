#ifndef ReleaseVersion
  #error ReleaseVersion is required
#endif
#ifndef Payload
  #error Payload is required
#endif

[Setup]
AppId={{26C20972-B76B-4CB8-B931-205F0B1CB23D}
AppName=Slepok
AppVersion={#ReleaseVersion}
AppPublisher=Slepok contributors
AppPublisherURL=https://github.com/miloVV13/slepok
DefaultDirName={localappdata}\Programs\Slepok
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
ChangesEnvironment=yes
LicenseFile={#Payload}\LICENSE
OutputBaseFilename=slepok-{#ReleaseVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
UninstallDisplayIcon={app}\slepok.exe
CloseApplications=no

[Tasks]
Name: addtopath; Description: "Add Slepok to your user PATH (open a new terminal after installation)"; Flags: checkedonce

[Files]
Source: "{#Payload}\slepok.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\INSTALL.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\release.json"; DestDir: "{app}"; Flags: ignoreversion

[Code]
const
  OwnerKey = 'Software\Slepok\Installer';

function NormalPath(Value: String): String;
begin
  Value := Trim(Value);
  if (Length(Value) >= 2) and (Value[1] = '"') and (Value[Length(Value)] = '"') then
    Value := Copy(Value, 2, Length(Value) - 2);
  while (Length(Value) > 3) and (Value[Length(Value)] = '\') do
    Delete(Value, Length(Value), 1);
  Result := Lowercase(Value);
end;

function ContainsPath(Value, Wanted: String): Boolean;
var
  Part: String;
  N: Integer;
begin
  Result := False;
  repeat
    N := Pos(';', Value);
    if N = 0 then N := Length(Value) + 1;
    Part := Copy(Value, 1, N - 1);
    Delete(Value, 1, N);
    if NormalPath(Part) = NormalPath(Wanted) then begin
      Result := True;
      Exit;
    end;
  until Value = '';
end;

procedure RemoveOwnedPath;
var
  Value, Owned, Part, Updated: String;
  N: Integer;
  More, Kept: Boolean;
begin
  if not RegQueryStringValue(HKCU, OwnerKey, 'Path', Owned) then Exit;
  RegQueryStringValue(HKCU, 'Environment', 'Path', Value);
  Updated := '';
  Kept := False;
  repeat
    N := Pos(';', Value);
    More := N <> 0;
    if N = 0 then N := Length(Value) + 1;
    Part := Copy(Value, 1, N - 1);
    Delete(Value, 1, N);
    if NormalPath(Part) <> NormalPath(Owned) then begin
      if Kept then Updated := Updated + ';';
      Updated := Updated + Part;
      Kept := True;
    end;
  until not More;
  if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Updated) then
    RaiseException('Cannot update user PATH');
  RegDeleteValue(HKCU, OwnerKey, 'Path');
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Value, Owned, App: String;
begin
  if CurStep <> ssPostInstall then Exit;
  App := ExpandConstant('{app}');
  if not WizardIsTaskSelected('addtopath') then begin
    RemoveOwnedPath;
    Exit;
  end;
  if RegQueryStringValue(HKCU, OwnerKey, 'Path', Owned) then
    if NormalPath(Owned) <> NormalPath(App) then RemoveOwnedPath;
  RegQueryStringValue(HKCU, 'Environment', 'Path', Value);
  if not ContainsPath(Value, App) then begin
    if Value <> '' then Value := Value + ';';
    if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Value + App) then
      RaiseException('Cannot update user PATH');
    if not RegWriteStringValue(HKCU, OwnerKey, 'Path', App) then
      RaiseException('Cannot record PATH ownership');
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then begin
    RemoveOwnedPath;
    RegDeleteKeyIfEmpty(HKCU, OwnerKey);
  end;
end;
