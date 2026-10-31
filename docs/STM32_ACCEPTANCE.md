# STM32F103C8 Blue Pill acceptance

Target: STM32F103C8T6, 64 KiB Flash at `0x08000000`, 20 KiB SRAM at
`0x20000000`; Cortex-M3. Example: active-low PC13 LED, HSI 8 MHz,
500 ms toggle interval. No external oscillator is required.

The MCU name selects a profile, separately from the host `--platform`.
The generated lock carries GCC/CMake/Ninja, optional OpenOCD, the three
STM32 SDK components, and Intel macOS's zstd runtime where needed.
It must not contain Pico SDK, Python, pioasm or picotool.

## Automated checks

```sh
go test -race ./...
go vet ./...
go build -o /tmp/slepok ./cmd/slepok
bash scripts/stm32_smoke.sh /tmp/slepok
```

The smoke script compares BIN before/after a restore into an empty store,
and checks the receiver's dry-run plan selects `stm32f1x.cfg`. Linux runs
the whole receiver process tree in a network namespace without external
network access. macOS does not enforce network isolation. The Windows
equivalent is `scripts/stm32_smoke.ps1 -Slepok C:\path\slepok.exe`;
it also does not enforce network isolation. No automated test programs
hardware. Use a separate test project/store for acceptance.

## Native manual build and migration

1. Check `slepok version` identifies the intended binary and host.
2. Run `slepok init --mcu stm32f103c8 bluepill`, enter `bluepill`, run
   `slepok build`. Check `build/bluepill.elf`, `.bin`, `.hex` exist.
3. Run `slepok flash --dry-run --cmsis-dap-quirk`: inspect the pinned
   OpenOCD, `stm32f1x.cfg`, STM32 ELF, verify/reset flags and 1000 kHz.
4. Pack normally for the current host. For another host, prepare and pack
   explicitly, e.g. `init --platform linux-x64,windows-x64` and
   `pack --platform linux-x64,windows-x64 -o bluepill.slepok`.
5. On a clean receiver without internet, unpack and build. Do not run
   init there. Compare BIN SHA256 with the sender. Also inspect the new
   dry-run plan for receiver store paths.

PowerShell invocation is `& $exe ...`, where `$exe` names the new binary.
No `SLEPOK_HOME` or global SDK/PATH setup is required for ordinary use.

## Hardware acceptance — pending

Use a board whose actual MCU matches STM32F103C8T6. SWDIO is PA13 and
SWCLK is PA14; share GND and provide 3.3 V target power. Leave BOOT0 low
for normal Flash boot. Follow the programmer firmware's output pin map.
CMSIS-DAP access may need the USB setup documented in README.

Run `slepok flash --no-build --cmsis-dap-quirk` when using the older
Picoprobe from the RP2040 acceptance. Expect programming, `Verified OK`
and target reset; check the PC13 LED toggles and still does after power
cycling. This writes target Flash. If communication fails, retain the
complete log, probe identity, board marking, binary version and clock.

Record native host, MCU marking, probe firmware, SHA256 and the exact
programming log. Native STM32 migration/hardware results must be tracked
separately from the already accepted RP2040 results. CMSIS-DAP is the
only exposed adapter selection in this first STM32 integration; RP2040's
RAM execution mode does not apply to STM32 yet.
