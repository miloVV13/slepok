param([Parameter(Mandatory=$true)][string]$Installer,
      [Parameter(Mandatory=$true)][string]$Version)
$ErrorActionPreference = 'Stop'
$root = Join-Path $env:RUNNER_TEMP 'slepok installer test'
$app = Join-Path $root 'application'
$reg = 'HKCU:\Environment'
$original = Get-ItemPropertyValue $reg Path -ErrorAction SilentlyContinue
$baseline = ';%USERPROFILE%\keep-before;;C:\Keep After;'
$store = Join-Path $env:LOCALAPPDATA 'slepok'
New-Item -ItemType Directory -Force $store | Out-Null
$sentinel = Join-Path $store 'installer-test-sentinel'
Set-Content $sentinel 'keep'

function Run-Setup {
    $p = Start-Process -Wait -PassThru $Installer -ArgumentList @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/DIR=`"$app`"", '/TASKS=addtopath')
    if ($p.ExitCode -ne 0) { throw "Setup exited $($p.ExitCode)" }
}
function Run-Uninstall {
    $p = Start-Process -Wait -PassThru (Join-Path $app 'unins000.exe') -ArgumentList '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART'
    if ($p.ExitCode -ne 0) { throw "Uninstall exited $($p.ExitCode)" }
}
try {
    New-ItemProperty $reg Path -Value $baseline -PropertyType ExpandString -Force | Out-Null
    Run-Setup
    Run-Setup
    $value = (Get-Item $reg).GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($value -ne "$baseline;$app") { throw "PATH was not preserved: $value" }
    $env:PATH = "$app;$env:PATH"
    $output = & slepok version
    if ($LASTEXITCODE -ne 0 -or $output[0] -notlike "slepok $Version *") { throw "Unexpected version: $output" }
    Run-Uninstall
    $value = (Get-Item $reg).GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($value -ne $baseline) { throw "Uninstall damaged PATH: $value" }
    if (!(Test-Path $sentinel)) { throw 'Uninstall removed SDK store' }
    # A user-owned PATH entry must remain after uninstall.
    New-ItemProperty $reg Path -Value "$baseline;$app" -PropertyType ExpandString -Force | Out-Null
    Run-Setup
    Run-Uninstall
    $value = (Get-Item $reg).GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($value -ne "$baseline;$app") { throw 'Uninstall removed a preexisting PATH entry' }
    Write-Host 'Windows install, upgrade, PATH ownership and uninstall checks passed'
} finally {
    if ($null -eq $original) { Remove-ItemProperty $reg Path -ErrorAction SilentlyContinue }
    else { New-ItemProperty $reg Path -Value $original -PropertyType ExpandString -Force | Out-Null }
    Remove-Item $sentinel -ErrorAction SilentlyContinue
}
