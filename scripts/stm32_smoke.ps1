param([Parameter(Mandatory=$true)][string]$Slepok)
$ErrorActionPreference = 'Stop'
$script:slepokExe = (Resolve-Path $Slepok).Path
function Invoke-Slepok {
    & $script:slepokExe @args
    if ($LASTEXITCODE -ne 0) { throw "slepok failed: $args (exit $LASTEXITCODE)" }
}
$savedHome = $env:SLEPOK_HOME
$savedLocation = Get-Location
$work = Join-Path ([IO.Path]::GetTempPath()) ('slepok-stm32-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
    $env:SLEPOK_HOME = Join-Path $work 'store a'
    Invoke-Slepok init --mcu stm32f103c8 (Join-Path $work 'bluepill')
    Set-Location (Join-Path $work 'bluepill')
    if (Select-String -Path slepok.lock -Pattern 'name = "(pico-sdk|python|pioasm|picotool)"') { throw 'Unexpected RP2040 package' }
    Invoke-Slepok build
    $plan = Invoke-Slepok flash --dry-run --cmsis-dap-quirk
    if ($plan -notmatch 'stm32f1x.cfg') { throw 'Wrong STM32 OpenOCD target' }
    Invoke-Slepok pack -o (Join-Path $work 'bluepill.slepok')
    $original = (Get-FileHash -Algorithm SHA256 build/bluepill.bin).Hash
    $env:SLEPOK_HOME = Join-Path $work 'store b'
    Invoke-Slepok validate (Join-Path $work 'bluepill.slepok')
    Invoke-Slepok unpack (Join-Path $work 'bluepill.slepok') (Join-Path $work 'restored')
    Set-Location (Join-Path $work 'restored')
    Invoke-Slepok build
    $plan = Invoke-Slepok flash --dry-run --cmsis-dap-quirk
    # Go prints native Windows paths as escaped quoted strings.
    $escapedReceiver = $env:SLEPOK_HOME.Replace('\', '\\')
    if ($plan -notmatch 'stm32f1x.cfg' -or -not $plan.Contains($escapedReceiver)) { throw 'Wrong receiver Flash plan' }
    if ((Get-FileHash -Algorithm SHA256 build/bluepill.bin).Hash -ne $original) { throw 'Restored BIN differs' }
    Write-Host 'STM32 native Windows restore/build passed; BIN identical. Network isolation was not enforced.'
} finally {
    Set-Location $savedLocation
    $env:SLEPOK_HOME = $savedHome
    Remove-Item -LiteralPath $work -Recurse -Force
}
