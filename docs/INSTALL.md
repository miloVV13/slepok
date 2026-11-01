# Installing Slepok

Download assets from [GitHub Releases](https://github.com/miloVV13/slepok/releases).
Select the newest release explicitly: early releases are marked **Pre-release**.
Go is only needed when building Slepok from source. Git is needed for `init`
when preparing RP2040 or ESP32 SDK sources.

## Windows 10/11 x64

Download `slepok-VERSION-windows-x64-setup.exe` and run it. Installation is
for the current user, requires no administrator privileges, and adds Slepok
to the user PATH by default. Open a new terminal and run:

```powershell
slepok version
slepok init --mcu rp2040 blinky
```

The installer is currently unsigned. Update by running a newer installer.
Remove it through Settings → Apps → Slepok. The SDK store in
`%LOCALAPPDATA%\slepok`, projects and `.slepok` files are preserved.
The application itself is installed in `%LOCALAPPDATA%\Programs\Slepok`.

## Linux x86-64

Download `slepok-VERSION-linux-x64.tar.gz`, extract it, and run the included
installer from the extracted directory:

```sh
tar -xzf slepok-VERSION-linux-x64.tar.gz -C /path/to/empty-directory
cd /path/to/empty-directory
bash install.sh
```

It installs into `~/.local/lib/slepok` and links `~/.local/bin/slepok`.
It adds that bin directory to bash, zsh and fish startup configuration.
Open a new terminal and run `slepok version`. No sudo or SDK environment
variables are needed. The extracted archive can also be installed offline.

For an online installation, the release's separate `install.sh` downloads
the archive for its own version and verifies it against the release
`SHA256SUMS`. Download that script from the selected release and run
`bash install.sh` in an empty directory. This needs curl and internet access.

Update by running the newer release's installer. Use `--prefix /absolute/path`
for another location, or `--no-modify-path` to manage PATH yourself.
Uninstall by removing `~/.local/bin/slepok` and `~/.local/lib/slepok`,
the `# >>> slepok PATH >>>` … `# <<< slepok PATH <<<` blocks from shell
startup files, and `~/.config/fish/conf.d/slepok.fish` (under
`$XDG_CONFIG_HOME` if set). Keep `~/.local/share/slepok`: it contains the
SDK store, separate from the application. Projects and snapshots are elsewhere
and are never removed by the installer.

## Portable use and macOS

Each platform has an archive: Linux x64, Windows x64, macOS arm64 and macOS x64.
Extract anywhere and run `./slepok` or `./slepok.exe` (`.\slepok.exe` in
PowerShell). No installation, PATH edits or environment setup is required.
Keep the accompanying license notices when copying the executable.
Portable refers to the application; the SDK store still uses the normal
per-user location unless `SLEPOK_HOME` is explicitly set.

macOS support is experimental. The Unix installer can also install the macOS
archive per user; there is no signed/notarized macOS installer yet.

## Checksums and requirements

The release's `SHA256SUMS` covers all downloaded assets. Verify the desired
asset with `sha256sum`/`shasum -a 256`, or `Get-FileHash -Algorithm SHA256`
on Windows. Every portable archive also contains its own payload checksums,
licenses and `release.json` with the source commit and build toolchain.
Checksums detect corruption; they are not a separate signature.

SDK tools need Linux glibc 2.34+ (Ubuntu 22.04+, Debian 12+), Windows 10/11
x64 or macOS 15. Initial preparation downloads packages; restoring a complete
snapshot and building it works offline. Existing hardware acceptance limits
are listed in README and the individual MCU documentation.

Slepok updates do not rewrite project lockfiles or existing snapshots.
Before version 1.0 the snapshot format may change; keep the older portable
executable alongside important snapshots when upgrading.
