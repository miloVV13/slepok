#!/usr/bin/env bash
# Native RP2040 round trip. macOS does not yet enforce network isolation.
# Usage: bash scripts/macos_smoke.sh /absolute/path/to/slepok
set -euo pipefail

case "${1:?pass the slepok binary}" in
    /*) slepok_bin=$1 ;;
    *) slepok_bin="$PWD/$1" ;;
esac
if [[ $(uname -s) != Darwin ]]; then
    echo 'This test requires native macOS.' >&2
    exit 1
fi
smoke_dir=$(mktemp -d "${TMPDIR:-/tmp}/slepok-roundtrip.XXXXXX")
smoke_dir=$(cd "$smoke_dir" && pwd -P)
cleanup() {
    chmod -R u+w "$smoke_dir"
    rm -rf -- "$smoke_dir"
}
trap cleanup EXIT

# Overrides are internal to this test, to simulate two separate machines.
# Ordinary users do not need to configure SLEPOK_HOME.
export SLEPOK_HOME="$smoke_dir/store a"
if [[ -n "${SLEPOK_SMOKE_DOWNLOADS:-}" ]]; then
    mkdir -p "$SLEPOK_HOME/downloads"
    cp "$SLEPOK_SMOKE_DOWNLOADS/"* "$SLEPOK_HOME/downloads/"
fi
"$slepok_bin" init "$smoke_dir/blink"
cd "$smoke_dir/blink"
"$slepok_bin" build
test -s build/compile_commands.json
"$slepok_bin" pack -o "$smoke_dir/blink.slepok"

export SLEPOK_HOME="$smoke_dir/store b"
"$slepok_bin" validate "$smoke_dir/blink.slepok"
"$slepok_bin" unpack "$smoke_dir/blink.slepok" "$smoke_dir/restored"
cd "$smoke_dir/restored"
"$slepok_bin" build
test -s build/compile_commands.json
if ! grep -Fq "$SLEPOK_HOME/" build/compile_commands.json; then
    echo 'Restored compilation database does not refer to the receiver store.' >&2
    exit 1
fi
if grep -Fq "$smoke_dir/store a/" build/compile_commands.json; then
    echo 'Restored compilation database refers to the original store.' >&2
    exit 1
fi

shopt -s nullglob
firmware=("$smoke_dir/blink/build/"*.uf2)
if (( ${#firmware[@]} == 0 )); then
    echo 'No UF2 firmware produced.' >&2
    exit 1
fi
for original in "${firmware[@]}"; do
    cmp "$original" "$smoke_dir/restored/build/$(basename "$original")"
done
echo 'Native macOS RP2040 restore and build passed; UF2 files are identical.'
echo 'Network isolation was not enforced by this test.'
