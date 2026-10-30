#!/usr/bin/env bash
# Real RP2040 round trip. Requires Linux, git and util-linux unshare.
# Usage: bash scripts/offline_smoke.sh /absolute/path/to/slepok
set -euo pipefail

slepok_bin=$(realpath "${1:?pass the slepok binary}")
smoke_dir=$(mktemp -d)
cleanup() {
    chmod -R u+w "$smoke_dir"
    rm -rf -- "$smoke_dir"
}
trap cleanup EXIT

# Fail early if offline execution cannot be enforced; no fallback in this test.
unshare --user --map-current-user --net true
export SLEPOK_HOME="$smoke_dir/store-a"
"$slepok_bin" init "$smoke_dir/blink"
cd "$smoke_dir/blink"
"$slepok_bin" build
"$slepok_bin" pack -o "$smoke_dir/blink.slepok"

# The receiver gets the archive and an empty store. Its entire process tree
# has no external network, including unpack (independent of runner fallback).
export SLEPOK_HOME="$smoke_dir/store-b"
unshare --user --map-current-user --net bash -eu -c '
    cd "$1"
    "$2" validate blink.slepok
    "$2" unpack blink.slepok restored
    cd restored
    "$2" build
' bash "$smoke_dir" "$slepok_bin"

# Compare the same source and lock after two clean builds in different stores.
shopt -s nullglob
firmware=("$smoke_dir/blink/build/"*.uf2)
if (( ${#firmware[@]} == 0 )); then
    echo 'No UF2 firmware produced' >&2
    exit 1
fi
for original in "${firmware[@]}"; do
    cmp "$original" "$smoke_dir/restored/build/$(basename "$original")"
done
echo 'RP2040 offline restore and build passed; UF2 files are identical.'
