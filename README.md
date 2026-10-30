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

> **Status: early.** Works on Linux x86-64 and Windows x86-64 with one profile, Raspberry Pi
> RP2040 (Pico SDK 2.3.1, ARM GCC 14.2). The snapshot format may still
> change before 1.0. Windows → Windows and Windows → Linux restore/build
> have been checked on real machines with matching firmware SHA256.
> macOS, STM32 and ESP32 are planned.

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
Debian 12+, Fedora 35+), or Windows 10/11 x64; `git` for `slepok init` only.

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
| `slepok init [--profile rp2040] [--board pico\|pico_w] [dir]` | Creates a blink project in an empty directory, or adds Slepok to an existing one, then downloads and verifies the environment. Needs network unless the packages are already in the store. |
| `slepok build` | Configures and builds the project without network access. |
| `slepok shell` | Opens your shell with the environment first in `PATH`. Running `cmake -S . -B build` by hand configures the project exactly like `slepok build`. Not network-isolated. |
| `slepok pack [-o file] [-with-sources]` | Builds a clean copy offline, then writes the snapshot. Refuses files that look like secrets unless `-allow-sensitive`. `-with-sources` bundles the source code of GPL components (see Licenses). |
| `slepok unpack <file> [dir]` | Verifies and restores a snapshot. No network needed. |
| `slepok validate <file>` | Checks every size and checksum without extracting or running anything. |
| `slepok inspect <file>` | Shows what a snapshot contains, reading only its manifest. |
| `slepok list` | Shows known projects and how much space the store uses. |
| `slepok gc [-n] [-y]` | Removes packages no known project uses, unused downloads and leftovers. Shows the plan first; never touches project sources. |

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
- **Not covered:** flashing and debugging. For RP2040, hold BOOTSEL and
  copy the `.uf2` from the build directory onto the `RPI-RP2` drive.

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

## Compared to

- **Nix / Guix** — stronger reproducibility, but a steep learning curve and
  no native Windows.
- **PlatformIO** — pins versions, but downloads packages from the network;
  no single offline file.
- **Dev Containers + `docker save`** — the manual version of the same idea;
  needs Docker on every machine and Linux-only containers.

Slepok's niche: one offline file, a proven restore, no Docker, any editor.

## Roadmap

macOS hosts · STM32 and ESP32 profiles · Python packages for
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
