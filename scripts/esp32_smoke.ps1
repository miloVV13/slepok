param([Parameter(Mandatory=$true)][string]$Slepok)
$ErrorActionPreference = 'Stop'
$script:slepokExe = (Resolve-Path $Slepok).Path
function Invoke-Slepok {
    & $script:slepokExe @args
    if ($LASTEXITCODE -ne 0) { throw "slepok failed: $args (exit $LASTEXITCODE)" }
}
$savedHome = $env:SLEPOK_HOME
$savedLocation = Get-Location
$work = Join-Path ([IO.Path]::GetTempPath()) ('slepok-esp32-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
    $env:SLEPOK_HOME = Join-Path $work 'store a'
    Invoke-Slepok init --mcu esp32 (Join-Path $work 'hello')
    Set-Location (Join-Path $work 'hello')
    if (Select-String -Path slepok.lock -Pattern 'name = "(arm-gcc|pico-sdk|pioasm|picotool|openocd|riscv[^\"]*)"') { throw 'Unexpected tool for another MCU' }
    Invoke-Slepok build
    Invoke-Slepok flash --port __slepok_virtual_port__ --dry-run
    Invoke-Slepok monitor --port __slepok_virtual_port__ --dry-run
    Invoke-Slepok pack -o (Join-Path $work 'hello.slepok')
    $hashes = @{}
    foreach ($output in @('hello.bin', 'bootloader/bootloader.bin', 'partition_table/partition-table.bin')) {
        $hashes[$output] = (Get-FileHash -Algorithm SHA256 (Join-Path 'build' $output)).Hash
    }
    $env:SLEPOK_HOME = Join-Path $work 'store b'
    Invoke-Slepok validate (Join-Path $work 'hello.slepok')
    Invoke-Slepok unpack (Join-Path $work 'hello.slepok') (Join-Path $work 'restored')
    Set-Location (Join-Path $work 'restored')
    Invoke-Slepok build
    $flashPlan = Invoke-Slepok flash --port __slepok_virtual_port__ --dry-run
    $monitorPlan = Invoke-Slepok monitor --port __slepok_virtual_port__ --dry-run
    Invoke-Slepok ports --dry-run
    $escapedReceiver = $env:SLEPOK_HOME.Replace('\', '\\')
    if (-not $flashPlan.Contains($escapedReceiver) -or -not $monitorPlan.Contains($escapedReceiver)) { throw 'Serial plans do not use receiver packages' }
    $restoredBuild = (Join-Path $work 'restored/build').Replace('\', '\\')
    if (-not $flashPlan.Contains($restoredBuild)) { throw 'Flash plan does not use receiver firmware' }
    foreach ($output in $hashes.Keys) {
        if ((Get-FileHash -Algorithm SHA256 (Join-Path 'build' $output)).Hash -ne $hashes[$output]) { throw "Restored $output differs" }
    }
    Write-Host 'ESP32 native Windows restore/build passed; application, bootloader and partition table identical. Network isolation was not enforced.'
} catch {
    if ($env:SLEPOK_SMOKE_DIAGNOSTICS) {
        New-Item -ItemType Directory -Force -Path $env:SLEPOK_SMOKE_DIAGNOSTICS | Out-Null
        foreach ($name in @('hello', 'restored')) {
            foreach ($relative in @('build.ninja', 'CMakeFiles/rules.ninja', 'CMakeCache.txt', 'compile_commands.json', 'sdkconfig', 'toolchain/cflags', 'toolchain/cxxflags', 'toolchain/asmflags', 'toolchain/ldflags', 'hello.elf', 'hello.bin', 'bootloader/bootloader.elf', 'bootloader/bootloader.bin', 'partition_table/partition-table.bin')) {
                $source = Join-Path $work "$name/build/$relative"
                if (Test-Path $source) {
                    Copy-Item -LiteralPath $source -Destination (Join-Path $env:SLEPOK_SMOKE_DIAGNOSTICS ($name + '-' + $relative.Replace('/', '-')))
                }
            }
        }
        $logs = Join-Path $env:SLEPOK_HOME 'logs'
        if (Test-Path $logs) {
            Copy-Item -LiteralPath $logs -Destination (Join-Path $env:SLEPOK_SMOKE_DIAGNOSTICS 'logs') -Recurse
        }
    }
    throw
} finally {
    Set-Location $savedLocation
    $env:SLEPOK_HOME = $savedHome
    Remove-Item -LiteralPath $work -Recurse -Force
}
