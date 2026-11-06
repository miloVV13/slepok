#!/usr/bin/env bash
# Real CMake generator, pinned native extension, offline snapshot round trip.
set -euo pipefail
exe=$(cd "$(dirname "${1:?usage: python_smoke.sh /path/to/slepok}")" && pwd)/$(basename "$1")
work=$(mktemp -d "${TMPDIR:-/tmp}/slepok-python-smoke.XXXXXX")
echo "Python smoke work directory: $work"
mkdir "$work/source"
case "$(uname -s)/$(uname -m)" in
    Linux/x86_64) host=linux-x64 ;;
    Darwin/arm64) host=macos-arm64 ;;
    Darwin/x86_64) host=macos-x64 ;;
    *) echo 'Unsupported smoke-test host' >&2; exit 1 ;;
esac
cat > "$work/source/slepok.toml" <<TOML
profile = "rp2040"
board = "pico"
platforms = ["$host"]
build_dir = "build"
[python]
requirements = ["Jinja2==3.1.6"]
TOML
cat > "$work/source/CMakeLists.txt" <<'CMAKE'
cmake_minimum_required(VERSION 3.13)
include($ENV{PICO_SDK_PATH}/external/pico_sdk_import.cmake)
project(python_generator C CXX ASM)
pico_sdk_init()
find_package(Python3 REQUIRED COMPONENTS Interpreter)
add_executable(python_generator main.c)
target_link_libraries(python_generator pico_stdlib)
target_include_directories(python_generator PRIVATE "${CMAKE_CURRENT_BINARY_DIR}")
add_custom_command(OUTPUT "${CMAKE_CURRENT_BINARY_DIR}/generated.h"
    COMMAND "${Python3_EXECUTABLE}" "${CMAKE_CURRENT_SOURCE_DIR}/generate.py" "${CMAKE_CURRENT_BINARY_DIR}/generated.h"
    DEPENDS "${CMAKE_CURRENT_SOURCE_DIR}/generate.py" VERBATIM)
target_sources(python_generator PRIVATE "${CMAKE_CURRENT_BINARY_DIR}/generated.h")
pico_add_extra_outputs(python_generator)
CMAKE
cat > "$work/source/main.c" <<'C'
#include "pico/stdlib.h"
#include "generated.h"
int main(void) { for (;;) sleep_ms(GENERATED_DELAY); }
C
cat > "$work/source/generate.py" <<'PY'
from pathlib import Path
import sys
from jinja2 import Template
from markupsafe import _speedups  # Require the actual native extension.
Path(sys.argv[1]).write_text(Template('#define GENERATED_DELAY {{ delay }}\n').render(delay=250))
PY
offline=()
if command -v unshare >/dev/null && unshare -Urn true 2>/dev/null; then
    offline=(unshare -Urn)
    echo 'Network namespace isolation is enabled for receiver and repeat init.'
else
    echo 'Network namespace unavailable: this run does not prove network isolation.'
fi
cd "$work/source"
"$exe" init
cp slepok.lock "$work/original.lock"
"${offline[@]}" "$exe" init
cmp slepok.lock "$work/original.lock"
"$exe" build
"$exe" pack -o "$work/project.slepok"
"${offline[@]}" env SLEPOK_HOME="$work/receiver-store" "$exe" unpack "$work/project.slepok" "$work/restored"
cd "$work/restored"
"${offline[@]}" env SLEPOK_HOME="$work/receiver-store" "$exe" build
for extension in uf2 bin hex; do
    cmp "$work/source/build/python_generator.$extension" "$work/restored/build/python_generator.$extension"
done
echo "Python generator offline round trip passed; artifacts retained in $work"
