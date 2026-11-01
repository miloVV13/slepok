#!/usr/bin/env bash
# Classic ESP32 native build and restore. No hardware is accessed.
set -euo pipefail
repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
slepok_bin=$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")
smoke_dir=$(mktemp -d "${TMPDIR:-/tmp}/slepok-esp32.XXXXXX")
cleanup() { chmod -R u+w "$smoke_dir"; rm -rf -- "$smoke_dir"; }
trap cleanup EXIT
smoke_dir=$(cd "$smoke_dir" && pwd -P)
if [[ $(uname -s) == Linux ]]; then
    unshare --user --map-current-user --net true
fi
# Simulates two machines; ordinary use needs no SLEPOK_HOME setup.
export SLEPOK_HOME="${SLEPOK_ESP32_SOURCE_STORE:-$smoke_dir/store a}"
"$slepok_bin" init --mcu esp32 "$smoke_dir/hello"
cd "$smoke_dir/hello"
if grep -Eq 'name = "(arm-gcc|pico-sdk|pioasm|picotool|openocd|riscv[^\"]*)"' slepok.lock; then
    echo 'ESP32 lock unexpectedly contains tools for another MCU.' >&2
    exit 1
fi
"$slepok_bin" build
"$slepok_bin" flash --port __slepok_virtual_port__ --dry-run > "$smoke_dir/serial-flash-a.txt"
"$slepok_bin" monitor --port __slepok_virtual_port__ --dry-run > "$smoke_dir/monitor-a.txt"
if [[ ${SLEPOK_ESP32_PTY_TEST:-} == 1 ]]; then
    SLEPOK_ESP32_PROJECT="$PWD" go -C "$repo_dir" test -v ./internal/runner -run '^TestRealESP32(MonitorPTY|SerialBackends)$' -count=1 -timeout 45s
fi
"$slepok_bin" pack -o "$smoke_dir/hello.slepok"
export SLEPOK_HOME="$smoke_dir/store b"
if [[ $(uname -s) == Linux ]]; then
    unshare --user --map-current-user --net bash -eu -c '
        "$1" validate "$2/hello.slepok"
        "$1" unpack "$2/hello.slepok" "$2/restored"
        cd "$2/restored"
        "$1" build
    ' bash "$slepok_bin" "$smoke_dir"
else
    "$slepok_bin" validate "$smoke_dir/hello.slepok"
    "$slepok_bin" unpack "$smoke_dir/hello.slepok" "$smoke_dir/restored"
    cd "$smoke_dir/restored"
    "$slepok_bin" build
    echo 'This host did not enforce network isolation.'
fi
cd "$smoke_dir/restored"
"$slepok_bin" flash --port __slepok_virtual_port__ --dry-run > "$smoke_dir/serial-flash-b.txt"
"$slepok_bin" monitor --port __slepok_virtual_port__ --dry-run > "$smoke_dir/monitor-b.txt"
"$slepok_bin" ports --dry-run > "$smoke_dir/ports-b.txt"
grep -Fq "$smoke_dir/restored/build/" "$smoke_dir/serial-flash-b.txt"
grep -Fq "$smoke_dir/restored/build/" "$smoke_dir/monitor-b.txt"
if grep -Fq "$smoke_dir/hello/build/" "$smoke_dir/serial-flash-b.txt"; then
    echo 'Flash plan refers to producer images.' >&2
    exit 1
fi
for output in hello.bin bootloader/bootloader.bin partition_table/partition-table.bin; do
    test -s "$smoke_dir/hello/build/$output"
    cmp "$smoke_dir/hello/build/$output" "$smoke_dir/restored/build/$output"
done
if grep -Fq "$smoke_dir/hello/" "$smoke_dir/restored/build/compile_commands.json"; then
    echo 'Restored compilation database refers to the producer.' >&2
    exit 1
fi
echo 'ESP32 native restore/build passed; application, bootloader and partition table are identical.'
