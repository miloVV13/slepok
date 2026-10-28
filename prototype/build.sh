#!/usr/bin/env bash
# Stage 1 prototype: build a project with the Slepok environment only.
# The host environment is wiped (env -i) and, where user namespaces are
# available, the network is removed (unshare -rn).
#
# usage: build.sh [project_dir] [build_dir]   (env dir: $ENV_DIR or prototype/work/env)
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
src=$(cd "${1:-$here/blink}" && pwd)
build=${2:-$src/build}
E=$(cd "${ENV_DIR:-$here/work/env}" && pwd)

path="$E/arm-gcc/bin:$E/cmake/bin:$E/ninja"
[[ -d $E/python/bin ]] && path="$E/python/bin:$path"

isolate=()
if /usr/bin/unshare -rn /bin/true 2>/dev/null; then
    isolate=(/usr/bin/unshare -rn)
else
    echo "warning: network namespace unavailable, building with network" >&2
fi

run() {
    "${isolate[@]}" /usr/bin/env -i \
        HOME=/nonexistent LANG=C.UTF-8 PATH="$path" \
        PICO_SDK_PATH="$E/pico-sdk" \
        "$@"
}

run cmake -S "$src" -B "$build" -G Ninja \
    -DPICO_BOARD=pico \
    -DPICO_TOOLCHAIN_PATH="$E/arm-gcc" \
    -Dpioasm_DIR="$E/pico-sdk-tools/pioasm" \
    -Dpicotool_DIR="$E/picotool/picotool" \
    -DFETCHCONTENT_FULLY_DISCONNECTED=ON \
    -DPICO_SDK_FETCH_FROM_GIT=OFF \
    -DPython3_ROOT_DIR="$E/python" \
    -DCMAKE_FIND_USE_CMAKE_SYSTEM_PATH=OFF \
    -DCMAKE_EXPORT_COMPILE_COMMANDS=ON
run cmake --build "$build"

ls -l "$build"/*.uf2
