#!/usr/bin/env bash
# Distributed alongside the binary, and as a version-pinned release bootstrap.
set -euo pipefail
release='@VERSION@'
prefix=${HOME:?}/.local
modify_path=yes
while (($#)); do
    case $1 in
        --prefix) prefix=${2:?--prefix requires an absolute directory}; shift 2 ;;
        --no-modify-path) modify_path=no; shift ;;
        *) echo "Usage: bash install.sh [--prefix /absolute/path] [--no-modify-path]" >&2; exit 2 ;;
    esac
done
[[ $prefix = /* && $prefix != *$'\n'* && $prefix != *$'\r'* ]] || { echo 'Invalid prefix' >&2; exit 2; }
prefix=${prefix%/}
[[ -n $prefix ]] || { echo 'Cannot install into /' >&2; exit 2; }
case "$(uname -s)/$(uname -m)" in
    Linux/x86_64) platform=linux-x64 ;;
    Darwin/arm64) platform=macos-arm64 ;;
    Darwin/x86_64) platform=macos-x64 ;;
    *) echo 'Unsupported host' >&2; exit 1 ;;
esac
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
sum() {
    if command -v sha256sum >/dev/null; then sha256sum "$@";
    else shasum -a 256 "$@"; fi
}
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]:-/dev/null}")" && pwd)
if [[ ! -f $source_dir/slepok || ! -f $source_dir/SHA256SUMS ]]; then
    [[ $release != '@VERSION@' ]] || { echo 'Use the installer from a release archive' >&2; exit 1; }
    archive="slepok-${release#v}-$platform.tar.gz"
    url="https://github.com/miloVV13/slepok/releases/download/$release"
    curl --fail --location --proto '=https' --tlsv1.2 "$url/$archive" -o "$work/$archive"
    curl --fail --location --proto '=https' --tlsv1.2 "$url/SHA256SUMS" -o "$work/sums"
    expected=$(awk -v name="$archive" '$2 == name {print $1}' "$work/sums")
    [[ $expected =~ ^[a-f0-9]{64}$ ]] || { echo 'Missing archive checksum' >&2; exit 1; }
    actual=$(sum "$work/$archive" | cut -d ' ' -f 1)
    [[ $actual = "$expected" ]] || { echo 'Archive checksum mismatch' >&2; exit 1; }
    mkdir "$work/payload"
    tar -xzf "$work/$archive" -C "$work/payload"
    source_dir=$work/payload
fi
(cd "$source_dir" && sum -c SHA256SUMS)
"$source_dir/slepok" version
app=$prefix/lib/slepok
bin=$prefix/bin
mkdir -p "$bin"
if [[ -e $app && ! -f $app/.slepok-install ]]; then
    echo "Refusing to replace unrelated directory: $app" >&2; exit 1
fi
if [[ -e $bin/slepok || -L $bin/slepok ]]; then
    [[ -L $bin/slepok && $(readlink "$bin/slepok") = "$app/slepok" ]] || {
        echo "Refusing to replace unrelated command: $bin/slepok" >&2; exit 1;
    }
fi
mkdir -p "$app"
# Rename the executable so an update also works while an old process is running.
for file in slepok LICENSE THIRD_PARTY_NOTICES.md INSTALL.md release.json; do
    cp "$source_dir/$file" "$app/$file.new"
    mv -f "$app/$file.new" "$app/$file"
done
chmod 755 "$app/slepok"
touch "$app/.slepok-install"
ln -s "$app/slepok" "$work/slepok"
mv -f "$work/slepok" "$bin/slepok"
if [[ $modify_path = yes ]]; then
    # A managed block can be replaced without changing the user's other settings.
    posix_bin=$(printf '%s' "$bin" | sed "s/'/'\\\\''/g")
    block="$work/path-block"
    printf '# >>> slepok PATH >>>\ncase ":$PATH:" in *:'\''%s'\'':*) ;; *) export PATH='\''%s'\'':"$PATH" ;; esac\n# <<< slepok PATH <<<\n' "$posix_bin" "$posix_bin" > "$block"
    for rc in "$HOME/.bashrc" "$HOME/.profile" "${ZDOTDIR:-$HOME}/.zshrc"; do
        mkdir -p "$(dirname "$rc")"
        touch "$rc"
        sed '/^# >>> slepok PATH >>>$/,/^# <<< slepok PATH <<<$/{d;}' "$rc" > "$work/rc"
        if [[ -s $work/rc && -n $(tail -c 1 "$work/rc") ]]; then printf '\n' >> "$work/rc"; fi
        cat "$block" >> "$work/rc"
        cat "$work/rc" > "$rc"
    done
    # bash ignores .profile when .bash_profile or .bash_login exists.
    for rc in "$HOME/.bash_profile" "$HOME/.bash_login"; do
        if [[ -f $rc ]]; then
            sed '/^# >>> slepok PATH >>>$/,/^# <<< slepok PATH <<<$/{d;}' "$rc" > "$work/rc"
            if [[ -s $work/rc && -n $(tail -c 1 "$work/rc") ]]; then printf '\n' >> "$work/rc"; fi
            cat "$block" >> "$work/rc"
            cat "$work/rc" > "$rc"
            break
        fi
    done
    fish_dir=${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d
    mkdir -p "$fish_dir"
    fish_bin=$(printf '%s' "$bin" | sed "s/\\\\/\\\\\\\\/g; s/'/\\\\'/g")
    printf "# Managed by Slepok installer\nif not contains -- '%s' \$PATH\n    set -gx PATH '%s' \$PATH\nend\n" "$fish_bin" "$fish_bin" > "$fish_dir/slepok.fish"
fi
printf '\nInstalled %s. Open a new terminal and run: slepok version\n' "$bin/slepok"
