#!/usr/bin/env python3
"""Exercise shipped Unix installers in an isolated home, without SDK downloads."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import zipfile


def verify_files(files):
    for line in files["SHA256SUMS"].decode().splitlines():
        digest, name = line.split("  ", 1)
        assert hashlib.sha256(files[name]).hexdigest() == digest, name


def main():
    dist = Path(sys.argv[1]).resolve()
    for archive in sorted(dist.glob("*.tar.gz")) + sorted(dist.glob("*.zip")):
        if archive.suffix == ".zip":
            with zipfile.ZipFile(archive) as zf:
                files = {n: zf.read(n) for n in zf.namelist()}
        else:
            with tarfile.open(archive) as tf:
                files = {m.name: tf.extractfile(m).read() for m in tf.getmembers()}
        verify_files(files)
        manifest = json.loads(files["release.json"])
        assert manifest["platform"] in archive.name
        assert b"MIT License" in files["LICENSE"]
        assert b"Go standard library" in files["THIRD_PARTY_NOTICES.md"]
    with tempfile.TemporaryDirectory(prefix="slepok-install-") as tmp:
        root = Path(tmp)
        payload = root / "payload"
        payload.mkdir()
        with tarfile.open(next(dist.glob("*-linux-x64.tar.gz"))) as tf:
            tf.extractall(payload, filter="data")
        home = root / "home with 'quotes"
        home.mkdir()
        env = {**os.environ, "HOME": str(home), "XDG_CONFIG_HOME": str(home / "config"),
               "ZDOTDIR": str(home / "zsh")}
        (home / ".bashrc").write_text("# preserve without trailing newline")
        (home / ".bash_profile").write_text("# preserve login settings\n")
        store = home / ".local/share/slepok"
        store.mkdir(parents=True)
        (store / "sentinel").write_text("keep")
        script = payload / "install.sh"

        def install(*args, success=True):
            result = subprocess.run(["bash", str(script), *args], env=env, capture_output=True, text=True)
            assert (result.returncode == 0) == success, result.stdout + result.stderr

        install()
        install()  # upgrade/idempotency
        command = home / ".local/bin/slepok"
        assert command.is_symlink()
        subprocess.run([str(command), "version"], check=True)
        for rc in [home / ".bashrc", home / ".profile", home / ".bash_profile", home / "zsh/.zshrc"]:
            text = rc.read_text()
            assert text.count("# >>> slepok PATH >>>") == 1
            subprocess.run(["bash", "-n", str(rc)], check=True)
        assert "preserve without trailing newline\n" in (home / ".bashrc").read_text()
        path = subprocess.check_output(["bash", "--noprofile", "--norc", "-c",
               'source "$HOME/.profile"; command -v slepok'], env=env, text=True).strip()
        assert path == str(command), path
        if subprocess.run(["sh", "-c", "command -v fish"], capture_output=True).returncode == 0:
            path = subprocess.check_output(["fish", "-c", "command -s slepok"], env=env, text=True).strip()
            assert path == str(command), path
        # An unrelated existing command must survive a refused install.
        other = root / "unrelated"
        (other / "bin").mkdir(parents=True)
        (other / "bin/slepok").write_text("do not replace")
        install("--prefix", str(other), "--no-modify-path", success=False)
        assert (other / "bin/slepok").read_text() == "do not replace"
        # Corruption must be rejected before replacing the installed binary.
        binary = payload / "slepok"
        corrupt = payload / "corrupt-binary"
        corrupt.write_bytes(binary.read_bytes() + b"corrupt")
        corrupt.replace(binary)
        before = command.read_bytes()
        install(success=False)
        assert command.read_bytes() == before
        assert (store / "sentinel").read_text() == "keep"
    print("Release payloads and Unix installation checks passed")


if __name__ == "__main__":
    main()
