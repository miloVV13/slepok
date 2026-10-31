# Slepok

*Slepok* (Russian *слепок*, "a cast, an impression") packs an embedded
project together with its exact build environment — compiler, SDK, build
tools — into one `.slepok` file. A colleague restores it and builds the
same firmware **offline**, without installing an SDK or a toolchain.

```console
$ slepok init blinky          # new RP2040 project with a pinned environment
$ cd blinky && slepok build   # builds without network access
$ slepok pack                 # proves an offline build, writes blinky.slepok

# on another machine, no internet needed:
$ slepok unpack blinky.slepok && cd blinky && slepok build
```

No environment setup is required. `SLEPOK_HOME` is an optional store
override, useful for testing or choosing another disk. By default the
store is `~/.local/share/slepok` on Linux (`XDG_DATA_HOME` is respected)
and `%LOCALAPPDATA%\slepok` on Windows. Restored tools are selected by
Slepok automatically; no SDK installation or global `PATH` changes are needed.

> **Status: early.** RP2040 (Pico SDK 2.3.1, ARM GCC 14.2) works on
> Linux x86-64 and Windows x86-64. An experimental STM32F103C8 Blue Pill
> profile adds pinned CMSIS/HAL and a blink example. The snapshot format may still
> change before 1.0. Windows → Windows and Windows → Linux restore/build
> have been checked on real machines with matching firmware SHA256.
> Experimental macOS 15 support (Apple Silicon and Intel) has passed native
> RP2040 build/pack/restore tests with identical UF2 in CI. A clean offline
> Mac receiver still needs manual acceptance. STM32 hardware acceptance and ESP32 are pending.

## Why

An embedded project depends on much more than its sources: a vendor SDK
with submodules, a cross compiler, CMake, Ninja, Python. Versions drift,
download links die, and "it builds on my machine" becomes a week of setup
for the next person — or impossible a few years later.

Slepok makes the environment a pinned, verifiable, portable object:

- **One file** holds the project and every tool needed to build it.
- **Offline by proof, not by promise.** `slepok pack` builds a clean copy
  of the project before it writes the snapshot. With Linux network
  isolation available, a required download makes that build fail.
  If isolation is unavailable, Slepok warns that offline builds are unproven.
- **Nothing installed into the system.** Tools live in a local store and
  are only ever used through Slepok.
- **Checks for host dependencies.** Builds run with an empty environment;
  Slepok checks the CMake cache and generated Ninja rules for system tool
  paths (a system Python found by CMake, for example).
- **Any editor.** Slepok is a command-line tool. clangd picks up
  `build/compile_commands.json`.
- **No Docker.** Toolchains for microcontrollers are relocatable; Slepok
  runs them natively, which also keeps editor integration simple.

## Install

Requirements: Linux x86-64 with glibc 2.34 or newer (Ubuntu 22.04+,
Debian 12+, Fedora 35+), Windows 10/11 x64, or experimental macOS 15
(Apple Silicon / Intel); `git` when preparing the RP2040 SDK with `init`.

Build from a checkout with Go 1.25+. `CGO_ENABLED=0` makes a static
binary that runs on any Linux x86-64, so you can copy it to machines
without Go:

```console
$ CGO_ENABLED=0 go build -trimpath -o slepok ./cmd/slepok
```

Downloads honour `HTTPS_PROXY`; `git` uses its own proxy settings.
On Linux and Windows terminals, downloads update one progress line with a
bar, percentage, transferred bytes, speed and ETA. Redirected output keeps
plain, infrequent log lines; `TERM=dumb` also disables interactive updates.

If a download fails because a server is unreachable from your network,
the error message names the file, its sha256 and where to put it: fetch it
any other way (a browser, a VPN, another computer) and run the command
again. The checksum makes sure it is the right file.

Once the repository is published, `go install
github.com/miloVV13/slepok/cmd/slepok@latest` works too; release binaries
are planned.

## Commands

| Command | What it does |
|---|---|
| `slepok init [--mcu rp2040\|stm32f103c8] [--board board] [dir]` | Creates a blink project or adds Slepok to existing sources. Downloads and verifies only the selected MCU profile's packages. `--profile` remains an equivalent selection flag. |
| `slepok build [--ram]` | Configures and builds the project without network access. `--ram` builds and validates RP2040 `no_flash` firmware in `build_dir/ram`. |
| `slepok flash [--elf file] [--serial id] [--speed kHz] [--no-build] [--dry-run]` | Builds and programs the selected RP2040 or STM32F103C8 through CMSIS-DAP using locked OpenOCD, verifies the write and resets the target. `--dry-run` does not build or access USB. |
| `slepok run --ram [--no-build] [--dry-run] [--cmsis-dap-quirk]` | Builds a RAM ELF, loads and verifies it in RP2040 SRAM, then starts it without programming Flash. Supports the same probe/serial/speed/ELF options as `flash`. |
| `slepok shell` | Opens your shell with the environment first in `PATH`. Running `cmake -S . -B build` by hand configures the project exactly like `slepok build`. Not network-isolated. |
| `slepok pack [-o file] [-with-sources]` | Builds a clean copy offline, then writes the snapshot. Refuses files that look like secrets unless `-allow-sensitive`. `-with-sources` bundles the source code of GPL components (see Licenses). |
| `slepok unpack <file> [dir]` | Verifies and restores a snapshot. No network needed. |
| `slepok validate <file>` | Checks every size and checksum without extracting or running anything. |
| `slepok inspect <file>` | Shows what a snapshot contains, reading only its manifest. |
| `slepok list` | Shows known projects and how much space the store uses. |
| `slepok gc [-n] [-y]` | Removes packages no known project uses, unused downloads and leftovers. Shows the plan first; never touches project sources. |

## Flashing RP2040 and USB access

The RP2040 profile includes xPack OpenOCD 0.12.0-7, its runtime libraries,
and its target and adapter scripts. `init` pins the host package and
`pack` carries it for each selected platform. A restored project can
flash offline without installing OpenOCD globally.
`slepok shell` also exposes this OpenOCD and its scripts automatically.

```console
$ slepok init                     # existing project: add OpenOCD to older locks
$ slepok build
$ slepok flash --dry-run           # inspect the command; no USB access or build
$ slepok flash                     # rebuild, program ELF, verify, reset and exit
$ slepok flash --no-build          # use the existing single build/*.elf
$ slepok flash --no-build --elf build/blinky.elf --serial YOUR_PROBE_SERIAL --speed 1000
```

The default probe is `cmsis-dap`, with a 5000 kHz SWD clock. If the build
directory contains multiple ELF files, select one with `--elf`; relative
paths are resolved from the project root. `--elf` still builds first
unless `--no-build` or `--dry-run` is specified. Flashing does not download
tools. Old snapshots without OpenOCD continue to build and pack; repeat
`init` with network access and repack to add flashing support. Existing
compiler/SDK pins and the build timestamp are preserved.

If OpenOCD reports `CMSIS-DAP command mismatch`, try the compatibility
mode for programmers with packet-pipelining issues:

```console
$ slepok flash --no-build --cmsis-dap-quirk
```

This enables OpenOCD's [CMSIS-DAP quirk workarounds](https://openocd.org/doc/html/Debug-Adapter-Configuration.html),
including one USB command packet at a time. It is opt-in and may reduce
throughput. Lowering `--speed` alone does not fix USB packet ordering.
No automatic retry occurs after a failed flash. The attached Picoprobe
(`2e8a:000c`, USB `bcdDevice=1.01`) failed a read-only ROM transfer even
at 1000 kHz; repeated ROM/RAM reads passed with this mode at 5000 kHz.
The user subsequently programmed a real RP2040 rev 2 through the same
probe at 5000 kHz with `--no-build --cmsis-dap-quirk`: OpenOCD detected
4096 KiB Flash, completed programming, reported `Verified OK`, and reset
the target (2026-10-31). Application behavior has not yet been confirmed.

You can also copy the built `.uf2` to the target board's BOOTSEL USB
drive. Building, packing and restoring do not require a connected board
or USB driver setup.

SWD flashing requires a CMSIS-DAP programmer. An
RP2040 Zero used as a programmer needs firmware built for its board and
wiring; the board alone does not identify its USB protocol. With
compatible CMSIS-DAP firmware, connect its SWDIO, SWCLK and GND to the
powered target according to the programmer firmware's pin assignment.
See the [Raspberry Pi Debug Probe documentation](https://www.raspberrypi.com/documentation/microcontrollers/debug-probe.html)
and [debugprobe firmware project](https://github.com/raspberrypi/debugprobe).

USB access belongs to the receiving operating system and may require
one-time setup even when all flashing tools are inside the snapshot:

| Host | Programmer access |
|---|---|
| Linux | If the programmer is detected but access is denied, install the OpenOCD package's matching udev rules through an administrator, reload the rules and reconnect the programmer. Use the rule for the actual firmware's USB VID/PID; do not make every USB device globally writable. Slepok will not install rules or elevate itself automatically. |
| Windows | CMSIS-DAP v1 uses HID; CMSIS-DAP v2 uses a bulk USB interface that needs WinUSB. Compatible firmware may bind the driver automatically. If it does not, follow the programmer firmware's driver instructions and select its debug interface, keeping the UART interface's driver intact. |
| macOS | A compatible CMSIS-DAP programmer normally needs no separate USB driver. Manual flashing acceptance on macOS is still pending. |

USB drivers and udev rules are not environment variables: they do not
require setting `SLEPOK_HOME` or adding tools to the global `PATH`.
Linux SWD programming, verification and reset have passed on real
hardware with the Picoprobe and compatibility flag described above.
The user also confirmed successful native Windows flashing with the same
setup and compatibility mode (2026-10-31). macOS flashing and flashing
from an offline restored snapshot still require hardware acceptance.
Automated checks cover package integrity, native OpenOCD startup and
script parsing without USB access, literal argument handling, and
restored flashing plans. OpenOCD's GDB, Tcl and Telnet listeners are
disabled for the one-shot flash command; interactive debugging is not
part of this command.
Driver references: [CMSIS-DAP USB firmware requirements](https://arm-software.github.io/CMSIS-DAP/latest/dap_firmware.html),
[OpenOCD on Windows](https://github.com/openocd-org/openocd/blob/master/README.Windows.md),
and [xPack OpenOCD USB access setup](https://xpack-dev-tools.github.io/openocd-xpack/docs/install/).

## Selecting the target MCU

```console
$ slepok init --mcu rp2040 pico-blink
$ slepok init --mcu stm32f103c8 bluepill-blink
```

`--mcu` selects the MCU profile and its SDK. `STM32F103C8T6` is also
accepted as an alias for `stm32f103c8`, case-insensitively. `--board`
selects a supported board within that profile; `--platform` selects the
computer OS/architecture to include, independently of the MCU.

Only one MCU profile is prepared. A STM32 project does not download Pico
SDK, Python, pioasm or picotool. GCC/CMake/Ninja and OpenOCD already in
the local store are reused by digest. Packages for other projects can
coexist in that store without being included in this project's snapshot.
With no target flag, a new project defaults to RP2040 for compatibility;
an existing project keeps its saved target. `--profile` remains supported;
conflicting `--profile` and `--mcu` values are rejected.

## STM32 Blue Pill (experimental)

The first STM32 profile targets [**STM32F103C8T6**](https://www.st.com/en/microcontrollers-microprocessors/stm32f103c8.html), Cortex-M3, with the
documented 64 KiB Flash and 20 KiB SRAM. It does not depend on extra
undocumented Flash capacity sometimes found on Blue Pill boards.

```console
$ slepok init --mcu stm32f103c8 bluepill
$ cd bluepill
$ slepok build
$ slepok flash --dry-run --cmsis-dap-quirk
# Once the matching target board is connected:
$ slepok flash --no-build --cmsis-dap-quirk
```

The generated blink toggles PC13 every 500 ms using HAL and the reset-
default 8 MHz HSI clock. An external crystal, CubeIDE, CubeMX and Python
are not needed. The pinned SDK consists of STM32F1 HAL 1.1.10, CMSIS Device
4.3.5 and CMSIS Core 6.4.0. Outputs are ELF, BIN and HEX; the linker script
enforces the C8 memory sizes. SDK components retain their license files.

`flash` selects `stm32f1x.cfg` for this profile, defaults to 1000 kHz SWD,
and checks the ELF against the C8 memory ranges before opening USB.
The existing Pico Zero CMSIS-DAP programmer can provide SWD; connect
its configured SWDIO/SWCLK to PA13/PA14 and share GND with the powered
target. This initial integration accepts CMSIS-DAP; ST-Link selection
is not implemented. `build --ram` and `run --ram` remain RP2040-only.

Linux native compilation and offline restore/build are covered by the
STM32 smoke test, including identical BIN comparison and a dry-run plan
using the receiver's OpenOCD. Native Windows/macOS builds are included
in CI; their results and real Blue Pill programming need acceptance.
See [STM32 acceptance](docs/STM32_ACCEPTANCE.md).

## Running RP2040 firmware in RAM (experimental)

```console
$ slepok build --ram
$ slepok run --ram --dry-run --cmsis-dap-quirk
$ slepok run --ram --no-build --cmsis-dap-quirk
```

`run --ram` builds first unless `--no-build` or `--dry-run` is given.
`--elf path` selects an existing ELF; relative paths start at the project
root. `--dry-run` requires an existing ELF and never opens USB.

The RAM build uses Pico SDK `PICO_DEFAULT_BINARY_TYPE=no_flash` in a
separate `build_dir/ram` directory (normally `build/ram`). It does not edit
your CMake files or switch the normal build mode. Projects that explicitly
override the target binary type must select `no_flash` themselves; the
resulting ELF is checked rather than assumed to be suitable.

Before USB access, Slepok checks every load segment's physical and virtual
address against RP2040's 264 KiB SRAM, rejects overlapping/mixed Flash
images, and checks the Thumb entry point, stack and vector table. This
initial implementation requires an unstripped Pico SDK ELF with
`_entry_point` and `__vectors` symbols. It does not support RP2350, SRAM
aliases or external RAM. `flash` rejects RAM-only ELF images.

OpenOCD resets and halts the target, loads and verifies SRAM, initializes
core 0 startup registers and VTOR, and resumes at the ELF entry point.
Core 1 is left to the normal boot ROM/SDK startup rather than resumed via
OpenOCD SMP. No `program`, Flash erase/write, or reset-after-load command
is issued. The application can still write Flash if its own code does so.
RAM contents are lost on power-off; an ordinary reset boots the existing
Flash firmware rather than automatically relaunching the RAM image.

RAM compilation, ELF validation, native OpenOCD Tcl parsing with hardware
operations stubbed, and an offline snapshot restore/build with identical
RAM BIN have been checked on Linux. The user subsequently confirmed
successful hardware RAM execution following the Windows instructions.
Hardware RAM execution on Linux/macOS still needs acceptance. Ordinary
Flash programming remains available through `slepok flash`.

## Files in your project

Select snapshot host platforms with `--platform` (these are the computers
running the build, not the target MCU). With no flag, `pack` includes only
the current host, even when the project has several platforms configured.
For example:

```sh
slepok init --platform linux-x64,windows-x64 blinky
cd blinky
slepok pack --platform linux-x64,windows-x64
# Or include only the already prepared Windows environment:
slepok pack --platform windows-x64 -o windows.slepok
```

`init --platform` saves the selection plus the current host for verification
builds. On an existing project, `init` without the flag keeps its configured
platforms and ensures the current host is included. `pack --platform` changes
only the archive selection and its restored config, leaving the source
project config unchanged. It does not download missing packages: run
`init --platform` first. Verification executes only on the producer host;
other hosts still require native testing. Windows can prepare Linux packages
as canonical tar archives, preserving Unix permissions and links without
creating them on Windows. Combined snapshots can be prepared on either host;
Windows → Windows and Windows → Linux migrations have been checked on real machines.
The additional choices are `macos-arm64` and `macos-x64`; both Mac
architectures can share one snapshot. Intel GCC's zstd runtime is bundled
automatically; Homebrew is not required. See [macOS acceptance](docs/MACOS_ACCEPTANCE.md).

- **`slepok.toml`** — written by you: profile, board, platforms.

  ```toml
  profile = "rp2040"
  board = "pico"
  platforms = ["linux-x64"]
  build_dir = "build"
  ```

- **`slepok.lock`** — written by `slepok init`. **Commit it.** It pins every
  component: the vendor download and its sha256 (or git commit) and the
  digest of the resulting package. With it, `slepok init` on any machine
  produces bit-for-bit the same environment, or fails loudly.
- **`.slepokignore`** — optional, `.gitignore` syntax: files to leave out
  of snapshots. The build directory, `.git/`, caches and `*.slepok` are
  always left out.

## How it works

- Each component (toolchain, CMake, SDK, ...) becomes a **package**: a
  directory tree stored in a canonical tar format, so the same tree always
  has the same sha256 **digest**. Packages live once in a local store
  (`~/.local/share/slepok`, or `$SLEPOK_HOME`), shared by all projects and
  read-only.
- The built-in **profile** for each platform pins the sources twice: the
  vendor checksum and the expected package digest.
- A **snapshot** is a zstd-compressed tar: `manifest.json` first (format
  version, platforms, every file and package with size and digest), then
  the project files and packages. Unpacking verifies everything before the
  project directory appears; nothing partial is left behind on failure.
- On Linux, builds run in new user and network namespaces created by
  Slepok itself: only loopback, and your own uid (not namespace root), so
  the build cannot write into the read-only store. If the host forbids
  unprivileged user namespaces, Slepok warns and builds with network.
- `slepok gc` keeps every package referenced by the `slepok.lock` of a
  project Slepok knows about (any project you ran a Slepok command in).
  Packages younger than an hour are always kept, so a running `init` is
  safe.
- Concurrent commands share a process lock on the store; `gc` waits for
  exclusive access before scanning and deleting. Downloads of the same
  checksum, package installation and registry updates are serialized.
  Locks are released by the OS even after SIGKILL. Lock files under
  `.locks/` remain in place and must not be removed while Slepok is running.

The full specification, including design decisions and measurements, is in
[Slepok_Project_Specification.md](Slepok_Project_Specification.md)
(Russian).

## What is and is not guaranteed

- **Guaranteed:** a restored snapshot builds on a compatible host
  (same OS and architecture as listed by `slepok inspect`) with the same
  pinned tools, without network.
  This offline guarantee requires a verification build with network
  isolation; a fallback build on a host that forbids namespaces is unproven.
- **Observed, not promised:** identical firmware. The RP2040 blink builds
  bit-for-bit identically on different stores and directories. Builds
  get `SOURCE_DATE_EPOCH` from `slepok.lock` (the moment the environment
  was pinned), so the build date the Pico SDK embeds is the same
  everywhere; set `SOURCE_DATE_EPOCH` yourself to override it. Projects
  that embed absolute paths may still differ.
- **Hardware checked:** Linux SWD flashing with the Picoprobe and
  `--cmsis-dap-quirk` described above; the user also confirmed Windows
  flashing with that setup. Other hosts/probes and offline
  restored flashing still require hardware acceptance. Interactive
  debugging is not implemented. BOOTSEL and copying `.uf2` to `RPI-RP2`
  remain available independently.

## Security

- `validate` and `inspect` never run code from a snapshot, and every path
  is checked against traversal and symlink tricks.
- `build` and `pack` **run the project's build scripts** with your user's
  permissions. Treat a snapshot from someone you do not trust like their
  source code.
- Checksums detect corruption, not tampering: whoever can change a
  snapshot can change its manifest. Signatures are planned.

Ctrl+C or SIGTERM cancels `init`, `build`, `pack`, `unpack` and `validate`.
On Linux, cancellation stops the build/git process group and cleans up
operation scratch data. Verified packages and partial downloads are kept
for reuse. Interrupted commands exit with status 130. SIGKILL or a power
loss cannot run cleanup; `gc` removes old scratch data inside the store.

## Licenses

Slepok is MIT licensed. The binary includes Go modules under BSD and MIT
licenses; their texts are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
(regenerate with `scripts/third_party_notices.sh`).

**Snapshots contain third-party software** under its own licenses. Every
package keeps its license files, every snapshot carries
`reports/NOTICE.txt`, and `slepok inspect`, `pack` and `unpack` point out
what matters when you share one. This is not legal advice; in short, for
the RP2040 profile:

| Situation | |
|---|---|
| Use snapshots yourself or inside your team or company | fine |
| Give a snapshot to someone else, publish it | fine, but the ARM GNU Toolchain is GPL: you must offer its source code. `slepok pack -with-sources` bundles it (about 300 MB) so you do not depend on Arm keeping it online |
| Sell snapshots or ship them in a commercial product | check first: the Pico SDK includes **BTstack**, whose license allows only personal, non-commercial use without a commercial license from BlueKitchen, and **cyw43-driver**, which may only be used with Raspberry Pi chips |
| Sell devices with firmware built by Slepok | same as without Slepok: GCC's runtime library exception keeps your firmware free of the GPL; keep the BSD notices of the Pico SDK in your documentation |

Bundled sources come out with `tar --zstd -xf project.slepok sources/`.
OpenOCD is GPL-2.0-or-later and carries additional library licenses under
`distro-info/licenses`. `-with-sources` includes its pinned upstream
OpenOCD archive; it does not include all xPack build recipes, changes or
bundled dependency sources. Those must also be provided as required when
redistributing the tool. The package NOTICE links the
[matching xPack recipes](https://github.com/xpack-dev-tools/openocd-xpack/tree/v0.12.0-7.1).

## Compared to

- **Nix / Guix** — stronger reproducibility, but a steep learning curve and
  no native Windows.
- **PlatformIO** — pins versions, but downloads packages from the network;
  no single offline file.
- **Dev Containers + `docker save`** — the manual version of the same idea;
  needs Docker on every machine and Linux-only containers.

Slepok's niche: one offline file, a proven restore, no Docker, any editor.

## Roadmap

Clean offline macOS acceptance · STM32 and ESP32 profiles · Python packages for
project scripts · snapshot signatures · release binaries.

## Development checks

The Linux MVP acceptance evidence and remaining CI status are recorded in
[MVP_ACCEPTANCE.md](docs/MVP_ACCEPTANCE.md) (Russian).

Run `go test -race ./...` and `go vet ./...`. Tests use local HTTP servers;
they do not download the RP2040 environment.

On Linux, tests also fill a disposable 1 MiB tmpfs to check write failures
and preservation of existing data. This requires user/mount namespaces
and util-linux `unshare`; the test skips when they are unavailable.
Set `SLEPOK_REQUIRE_FILESYSTEM_TESTS=1` to require it (as CI does).

For a real Linux RP2040 round trip, build the binary and run
`bash scripts/offline_smoke.sh /absolute/path/to/slepok`. This downloads
the pinned environment into a temporary store, builds and packs blink,
then validates, restores and builds it in a second empty store inside
a network namespace. It also compares the UF2 files. The script requires
`git`, util-linux `unshare` and enabled unprivileged user namespaces;
it fails if network isolation is unavailable. Temporary data is removed
on exit. It verifies that `compile_commands.json` uses the restored store.
If clangd is available (or selected via `SLEPOK_CLANGD`), it also checks
the restored source using the ARM compiler through `--query-driver`.
Both checks run in GitHub Actions; the real-build job installs clangd
for this editor check, while the firmware still uses bundled tools.
