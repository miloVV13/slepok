#!/usr/bin/env bash
# Native STM32F103C8 round trip; hardware is never opened.
set -euo pipefail
slepok_bin=$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")
smoke_dir=$(mktemp -d)
cleanup() { chmod -R u+w "$smoke_dir"; rm -rf -- "$smoke_dir"; }
trap cleanup EXIT
smoke_dir=$(cd "$smoke_dir" && pwd -P)
if [[ "$(uname -s)" == Linux ]]; then
    unshare --user --map-current-user --net true
fi
export SLEPOK_HOME="${SLEPOK_STM32_SOURCE_STORE:-$smoke_dir/store a}"
"$slepok_bin" init --mcu stm32f103c8 "$smoke_dir/bluepill"
cd "$smoke_dir/bluepill"
if grep -Eq 'name = "(pico-sdk|python|pioasm|picotool)"' slepok.lock; then
    echo 'STM32 lock unexpectedly contains RP2040 packages' >&2
    exit 1
fi
"$slepok_bin" build
"$slepok_bin" flash --dry-run --cmsis-dap-quirk > "$smoke_dir/flash-a.txt"
grep -Fq stm32f1x.cfg "$smoke_dir/flash-a.txt"
"$slepok_bin" pack -o "$smoke_dir/bluepill.slepok"
export SLEPOK_HOME="$smoke_dir/store b"
if [[ "$(uname -s)" == Linux ]]; then
    unshare --user --map-current-user --net bash -eu -c '
        "$1" validate "$2/bluepill.slepok"
        "$1" unpack "$2/bluepill.slepok" "$2/restored"
        cd "$2/restored"
        "$1" build
        "$1" flash --dry-run --cmsis-dap-quirk > "$2/flash-b.txt"
    ' bash "$slepok_bin" "$smoke_dir"
else
    "$slepok_bin" validate "$smoke_dir/bluepill.slepok"
    "$slepok_bin" unpack "$smoke_dir/bluepill.slepok" "$smoke_dir/restored"
    cd "$smoke_dir/restored"
    "$slepok_bin" build
    "$slepok_bin" flash --dry-run --cmsis-dap-quirk > "$smoke_dir/flash-b.txt"
    echo 'This host did not enforce network isolation.'
fi
grep -Fq "$SLEPOK_HOME/" "$smoke_dir/flash-b.txt"
grep -Fq stm32f1x.cfg "$smoke_dir/flash-b.txt"
cmp "$smoke_dir/bluepill/build/bluepill.bin" "$smoke_dir/restored/build/bluepill.bin"
echo 'STM32 native restore/build passed; BIN identical; Flash plans use stm32f1x.'
