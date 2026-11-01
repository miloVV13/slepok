# Classic ESP32 / ESP-IDF

The first profile is `slepok init --mcu esp32 project-name`. It selects
ESP-IDF **5.5.6**, commit `880a203ae603d08a94d06aa01e0d6761958e5e9e`,
Espressif `xtensa-esp-elf` **esp-14.2.0_20260121**, CPython **3.12.16**,
CMake and Ninja. ROM ELFs **20260528** accompany the SDK's generated debugger
configuration; a debugger itself is not included.

The template uses the SDK's minimal component build and prints a message
once per second through FreeRTOS. It stores generated sdkconfig under the
build directory, enables ESP-IDF reproducible builds, disables wall-clock build dates and
remaps SDK, project, build and compiler paths in debug information.
It does not assume a board LED or access hardware. The initial profile
supports original ESP32 only; S2/S3 and RISC-V chips need separate profiles.
The vendor compiler archive shares Xtensa support, so its contents also
include S2/S3 tools. No additional RISC-V compiler is fetched.

## USB/UART flash and monitor

Run these commands from the ESP32 project. The port is explicit; Slepok does
not choose a connected device automatically.

```sh
slepok ports
slepok flash --port /dev/ttyUSB0 --dry-run
slepok flash --port /dev/ttyUSB0
slepok monitor --port /dev/ttyUSB0
```

In PowerShell, for example with `$exe` pointing to the new binary:

```powershell
& $exe ports
& $exe flash --port COM3 --dry-run
& $exe flash --port COM3
& $exe monitor --port COM3
```

`flash` builds first; `--no-build` uses existing images. `--dry-run` skips
building and never opens the port, but needs existing build artifacts.
The locked esptool reads addresses and settings from IDF's
`build/flasher_args.json`; the plan includes bootloader, partition table,
application and any additional ordinary images. Images are checked before
opening the device: files stay within the build directory, offsets/erase
sectors do not overlap and the main images identify classic ESP32. Encryption
and custom reset/no-stub modes are outside this initial serial integration.
A normal write verifies data using esptool's automatic MD5 check and resets
the target ([vendor documentation](https://docs.espressif.com/projects/esptool/en/release-v4/esp32/esptool/advanced-commands.html)).
The default flash baud rate is 460800; try `--baud 115200` if the link is
unreliable. SWD flags (`--probe`, `--serial`, `--speed`, `--elf`,
`--cmsis-dap-quirk`) apply to the ARM profiles, not ESP32 serial flashing.

`monitor` uses the locked IDF monitor and ELF for address decoding. It does
not build or flash. The default baud rate is 115200; override with `--baud`.
It does not request a startup reset unless `--reset` is supplied. Use
`--timestamps` for timestamps, `--elf path` to select an existing ELF,
Ctrl+] to exit and Ctrl+T then Ctrl+H for help. Build/flash menu shortcuts
are adapted to print the Slepok commands instead of invoking a host `make`.
GDB/debugger integration remains a future task. `monitor --dry-run` and
`ports --dry-run` only print their invocations. No environment activation,
user pip installation or SLEPOK_HOME override is required.

### USB access and board check

Use a data-capable USB cable and the board's USB/UART port. On Windows,
identify its COM port in Device Manager and install the matching bridge
vendor's serial driver if needed: [CP210x VCP](https://www.silabs.com/software-and-tools/usb-to-uart-bridge-vcp-drivers)
or [CH340/CH341](https://www.wch.cn/downloads/CH341SER_EXE.html).
On Linux, inspect the device's group with `ls -l /dev/ttyUSB0` and give your
user serial-port access through that group (commonly `dialout` on Ubuntu,
`uucp` on Arch/CachyOS); log in again after changing group membership.
On macOS choose the `/dev/cu.*` entry shown by `slepok ports`.
Close other terminals/IDEs that hold the same port. If auto-reset does not
enter the ROM loader, use the board's BOOT/EN buttons following its manual.

Hardware acceptance is still pending: on a classic ESP32 board run flash,
confirm all images report verified data, then open monitor and confirm
`Hello from Slepok on ESP32!` repeats. Exit with Ctrl+], reopen the monitor,
then repeat with `flash --no-build --baud 115200` and a restored snapshot.
Perform that check on native Windows and Linux when a board is available.

## Portable Python

The interpreter is the same shared, immutable component used by RP2040.
`esp-idf-python` is a separate library tree with explicit artifact URLs,
SHA256 values and a resulting package digest for each host. The catalog is
`internal/profile/components/esp-idf-python.toml`. `init` does not resolve
versions or invoke pip, setup.py, a compiler or the IDF installer.

Wheels are unpacked with metadata, native extensions and licenses. Wheel
purelib/platlib data is relocated into the library root; script launchers
are omitted because builds invoke Python modules and SDK scripts directly.
The explicitly selected pure-Python directories and egg-info from the
pinned esptool 4.12.0 source archive are copied without executing setup.py.
Unsupported wheel installation schemes, links and conflicting files fail.
A relocatable sitecustomize processes the pinned wheels' .pth files as a
normal site-packages directory does. Slepok supplies PYTHONPATH and isolates
user site-packages for every build; the user configures no environment.

Before CMake runs, the pinned interpreter executes the SDK's dependency
checker against its core requirements. CMake's stock venv-layout check is
replaced by this check: the portable interpreter is not a virtualenv.
The component manager is disabled to prevent build-time downloads; external
managed components must already be vendored by the project.

Catalog preparation used the SDK's requirements.core.txt and the official
[5.5 constraints](https://dl.espressif.com/dl/esp-idf/espidf.constraints.v5.5.txt).
Runtime builds do not download that mutable constraints file. The concrete
versions and bytes are frozen in the catalog and copied into slepok.lock.
Windows additionally includes windows-curses for CPython 3.12. Each host's
library tree is assembled with identical canonical permissions regardless
of the preparing OS; foreign native libraries are copied, never executed.

SDK submodule selection includes classic ESP32 Bluetooth, Wi-Fi, PHY,
coexistence and the common SDK dependencies. Other-chip Bluetooth controller
and OpenThread submodules are omitted. Selected nested submodules are pinned
by their parent Git commits. The complete SDK source tree remains present;
this first profile does not promise every optional SDK feature. The supplied
minimal example is the acceptance baseline. Licenses stay in the SDK and
Python distributions. The compiler profile records Espressif's corresponding
source archive; the Python profile records the esptool source archive for
`pack --with-sources`.

On Windows Slepok resolves an available short directory alias for the
Xtensa package while retaining full tool filenames. This avoids CMake's
whole-filename shortening triggering the vendor wrapper's config-basename
mismatch (see [upstream issue](https://github.com/espressif/crosstool-NG/issues/68)).
The package's bytes remain unchanged; Windows resolves aliases back to real
paths when checking store boundaries. No user PATH or install-location
setting is required. The compiler directory alias is also remapped to
`/TOOLCHAIN` in debug information: IDF remaps its long spelling, whereas
GCC header line tables can retain the short spelling. This matters because
the application BIN embeds the ELF hash.

## Acceptance

The complete `init → build → pack → validate → unpack → build` round trip
passed on Linux x64, Windows x64, macOS Apple Silicon and macOS Intel in
[CI for c2e71c6](https://github.com/miloVV13/slepok/actions/runs/41306378502).
Application, bootloader and partition-table BIN files are byte-identical
between producer and receiver on each host. Linux used an empty receiver
store with network isolation; Windows/macOS did not enforce isolation.
The local Linux example snapshot is approximately 418 MiB. SDK Python
requirement checks, Go race tests and vet checks also pass.

Hardware acceptance and a manual cross-OS transfer have not been performed.
The earlier deferred STM32 hardware and manual macOS offline checks remain
open.

Automated checks (no hardware access):

```sh
bash scripts/esp32_smoke.sh /absolute/path/to/slepok
```

On Windows, from the repository in PowerShell:

```powershell
./scripts/esp32_smoke.ps1 -Slepok C:\path\to\slepok.exe
```

Both scripts use two separate stores and compare SHA256/bytes of the
application, bootloader and partition table after pack/validate/unpack/build.
Linux enforces network isolation for the receiver; Windows/macOS do not.
Their internal SLEPOK_HOME overrides simulate two machines; ordinary use
requires no override. CI runs these checks on all four supported hosts.
Manual Windows ↔ Linux migration and physical board acceptance remain
separate checks. Serial flash/monitor commands are implemented with metadata
and CLI dry-run tests; actual board programming has not been accepted yet.
An opt-in POSIX PTY test runs the real pinned IDF monitor/bridge, receives
UART text, checks the disabled flash shortcut, Ctrl+] exit and terminal
restoration. PTYs emulate DTR/RTS ioctls; this does not test physical reset
wiring or an actual ESP32. CI enables this test on Linux and macOS; it also
checks flash/monitor plans on both sides of native snapshot restoration.
