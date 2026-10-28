#!/usr/bin/env bash
# Stage 1 prototype: fetch a pinned, relocatable RP2040 build environment
# into $WORK/env (default: prototype/work/env). Needs network; nothing is
# installed into the system.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
source "$here/versions.env"
work=${WORK:-$here/work}
dl=$work/downloads
env=$work/env
lock=$here/downloads.sha256

mkdir -p "$dl" "$env"

fetch() { # url name
    local out=$dl/$2
    if [[ ! -f $out ]]; then
        echo ">> download $2"
        curl -fL --retry 8 --retry-all-errors --retry-delay 3 -o "$out.part" "$1"
        mv "$out.part" "$out"
    fi
}

fetch "$ARM_GCC_URL" arm-gcc.tar.xz
fetch "$ARM_GCC_URL.sha256asc" arm-gcc.tar.xz.sha256asc
fetch "$CMAKE_URL" cmake.tar.gz
fetch "$CMAKE_SUMS_URL" cmake-SHA-256.txt
fetch "$NINJA_URL" ninja.zip
fetch "$PICO_TOOLS_URL" pico-sdk-tools.tar.gz
fetch "$PICOTOOL_URL" picotool.tar.gz
fetch "$PYTHON_URL" python.tar.gz

echo ">> verify vendor checksums"
(cd "$dl"
 [[ $(sha256sum arm-gcc.tar.xz | cut -d' ' -f1) == $(cut -d' ' -f1 arm-gcc.tar.xz.sha256asc) ]] \
     || { echo "arm-gcc checksum mismatch" >&2; exit 1; }
 grep -q "$(sha256sum cmake.tar.gz | cut -d' ' -f1)  cmake-${CMAKE_VERSION}-linux-x86_64.tar.gz" cmake-SHA-256.txt \
     || { echo "cmake checksum mismatch" >&2; exit 1; }
 echo "$PYTHON_SHA256  python.tar.gz" | sha256sum -c --quiet \
     || { echo "python checksum mismatch" >&2; exit 1; })

# Components without vendor checksums are pinned on first fetch (trust on
# first use) and verified against the lock file afterwards.
echo ">> verify lock file"
(cd "$dl"
 files="arm-gcc.tar.xz cmake.tar.gz ninja.zip pico-sdk-tools.tar.gz picotool.tar.gz"
 if [[ -f $lock ]]; then
     sha256sum -c --quiet "$lock"
 else
     sha256sum $files > "$lock"
     echo "   created $lock"
 fi)

extract() { # archive dest [tar args...]
    local src=$dl/$1 dest=$env/$2; shift 2
    [[ -d $dest ]] && return
    echo ">> extract $dest"
    mkdir -p "$dest.part"
    tar -xf "$src" -C "$dest.part" "$@"
    mv "$dest.part" "$dest"
}

extract arm-gcc.tar.xz arm-gcc --strip-components=1
extract cmake.tar.gz cmake --strip-components=1
extract pico-sdk-tools.tar.gz pico-sdk-tools
extract picotool.tar.gz picotool
extract python.tar.gz python --strip-components=1

if [[ ! -d $env/ninja ]]; then
    echo ">> extract $env/ninja"
    mkdir -p "$env/ninja.part" && unzip -q "$dl/ninja.zip" -d "$env/ninja.part" && mv "$env/ninja.part" "$env/ninja"
fi

if [[ ! -d $env/pico-sdk ]]; then
    echo ">> clone pico-sdk $PICO_SDK_VERSION with submodules"
    # The network here drops TLS connections often, so retry each step
    # separately; submodules already fetched are kept between attempts.
    retry() { local i; for i in 1 2 3 4 5 6 7 8 9 10; do "$@" && return 0; echo "   retry $i: $*" >&2; sleep 3; done; return 1; }
    clone_sdk() { rm -rf "$env/pico-sdk.part"
        git -c advice.detachedHead=false clone -q --depth 1 --branch "$PICO_SDK_VERSION" "$PICO_SDK_GIT" "$env/pico-sdk.part"; }
    [[ -d $env/pico-sdk.part/.git ]] || retry clone_sdk || { echo "pico-sdk clone failed" >&2; exit 1; }
    retry git -C "$env/pico-sdk.part" submodule update -q --init --recursive --depth 1 \
        || { echo "pico-sdk submodules failed" >&2; exit 1; }
    # Git metadata is not needed to build and adds ~95 MB.
    find "$env/pico-sdk.part" -name .git -prune -exec rm -rf {} +
    mv "$env/pico-sdk.part" "$env/pico-sdk"
fi

echo ">> done: $env"
du -sh "$env"/* | sort -h
