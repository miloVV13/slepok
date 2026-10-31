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
if [[ -n "${SLEPOK_SMOKE_DOWNLOADS:-}" ]]; then
    mkdir -p "$SLEPOK_HOME/downloads"
    cp "$SLEPOK_SMOKE_DOWNLOADS/"* "$SLEPOK_HOME/downloads/"
fi
"$slepok_bin" init "$smoke_dir/blink"
cd "$smoke_dir/blink"
"$slepok_bin" build
test -s build/compile_commands.json
"$slepok_bin" build --ram
"$slepok_bin" run --ram --dry-run --cmsis-dap-quirk > "$smoke_dir/ram-plan-a.txt"
grep -Fq 'load_image' "$smoke_dir/ram-plan-a.txt"
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
    "$2" flash --dry-run > "$1/flash-plan.txt"
    grep -Fq "$SLEPOK_HOME/packages/" "$1/flash-plan.txt"
    grep -Fq "verify reset exit" "$1/flash-plan.txt"
    "$2" build --ram
    "$2" run --ram --dry-run --cmsis-dap-quirk > "$1/ram-plan-b.txt"
    grep -Fq "load_image" "$1/ram-plan-b.txt"
    grep -Fq "verify_image" "$1/ram-plan-b.txt"
    test -s build/compile_commands.json
    grep -Fq "$SLEPOK_HOME/packages/" build/compile_commands.json
' bash "$smoke_dir" "$slepok_bin"

if grep -Fq "$smoke_dir/store-a/" "$smoke_dir/restored/build/compile_commands.json"; then
    echo 'Restored compile_commands.json still refers to the original store' >&2
    exit 1
fi

# An optional editor check; clangd is a test tool, not a build dependency.
clangd_bin="${SLEPOK_CLANGD:-}"
if [[ -z "$clangd_bin" ]] && command -v clangd >/dev/null; then
    clangd_bin=clangd
fi
if [[ -n "$clangd_bin" ]]; then
    "$clangd_bin" --check="$smoke_dir/restored/main.c" \
        --compile-commands-dir="$smoke_dir/restored/build" \
        --query-driver="$SLEPOK_HOME/packages/*/bin/arm-none-eabi-*" --log=error
fi

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
ram_firmware=("$smoke_dir/blink/build/ram/"*.bin)
if (( ${#ram_firmware[@]} == 0 )); then
    echo 'No RAM firmware produced' >&2
    exit 1
fi
for original in "${ram_firmware[@]}"; do
    cmp "$original" "$smoke_dir/restored/build/ram/$(basename "$original")"
done
echo 'RP2040 offline restore and build passed; UF2 files are identical.'
echo 'RAM builds passed before and after restore; BIN files are identical.'
