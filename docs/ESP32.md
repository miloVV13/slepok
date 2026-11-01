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

## Acceptance

The local Linux `init → build → pack → validate → unpack → build` round
trip passed with an empty receiver store and network isolation. Application,
bootloader and partition-table BIN files are byte-identical. The example
snapshot is approximately 418 MiB. SDK Python requirement checks and Go
race tests pass. Native Windows/macOS CI is being checked; hardware
acceptance has not been performed. Do not treat a
cross-prepared package digest as evidence that its native binaries ran.

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
separate checks. ESP32 serial flashing, monitor and debugger commands will
be integrated after the build/restore baseline is accepted.
