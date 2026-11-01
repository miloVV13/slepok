Ready-to-use Slepok binaries for Linux x64, Windows x64, macOS Apple Silicon and Intel.

- Windows: run `*-windows-x64-setup.exe`, then open a new terminal and use `slepok`.
- Linux: extract `*-linux-x64.tar.gz` and run `bash install.sh`.
- Portable: extract the archive for your OS and run the included executable.
- No Go installation or SDK environment setup is required. SDKs are downloaded by `init`, or restored offline from a complete snapshot.

See [installation instructions](https://github.com/miloVV13/slepok/blob/main/docs/INSTALL.md) for checksums, updates and removal.
The Windows installer is unsigned. Application updates/removal preserve SDK stores, projects and snapshots.

This is an early release. RP2040 has been checked on Linux and Windows hardware.
STM32 and ESP32 hardware acceptance, manual ESP32 cross-OS acceptance and a clean offline macOS receiver check remain pending.
Snapshot compatibility may change before 1.0.
