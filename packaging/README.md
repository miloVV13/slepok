# Maintaining releases

Release workflow pins Go 1.27.3 and builds with CGO disabled. Python and Inno
Setup are maintainer tools only; users do not need either. Portable archives
include only Slepok and its linked-code licenses, not vendor SDKs/toolchains.
`third_party_notices.sh` regenerates notices using the actual release builder.

1. Push changes and run `gh workflow run release.yml -f version=vX.Y.Z-alpha.N`.
   This validates Linux installation, native Windows install/upgrade/uninstall
   and macOS portable execution, and retains the artifacts without publishing.
2. After successful checks, tag that commit and push the tag. `Release` repeats
   the checks, appends the Windows setup checksum, then publishes all assets.
   Tags containing `-` become GitHub prereleases; existing releases are never
   overwritten. Release tag names must match `vMAJOR.MINOR.PATCH[-prerelease]`.
3. Check the published downloads and `SHA256SUMS`. For important releases,
   perform the manual offline/hardware checks described in the MCU docs too.

Local packaging without publication:

```sh
GOTOOLCHAIN=go1.27.3 python3 scripts/release.py vX.Y.Z-alpha.N --out /tmp/slepok-release-check
python3 scripts/install_smoke.py /tmp/slepok-release-check
```

The output directory must be empty. Archives use sorted entries and fixed
timestamps from the source commit. `release.json` records the commit and Go
version; package checksums and release checksums have separate scopes.
The standalone Unix installer is pinned to the release that ships it, so
prerelease installation never depends on GitHub's stable-only `latest` URL.

Windows installer uses per-user mode and environment-change notification:
[Inno Setup privileges](https://jrsoftware.org/ishelp/topic_setup_privilegesrequired.htm),
[environment changes](https://jrsoftware.org/ishelp/topic_setup_changesenvironment.htm).
The Windows runner supplies [Inno Setup](https://github.com/actions/runner-images/blob/main/images/windows/Windows2022-Readme.md).
Signing/notarization and package-manager submissions are separate future work.
