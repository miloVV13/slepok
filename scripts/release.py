#!/usr/bin/env python3
"""Build portable release payloads using only Go and the Python standard library."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
HOSTS = {"linux-x64": ("linux", "amd64"), "windows-x64": ("windows", "amd64"),
         "macos-arm64": ("darwin", "arm64"), "macos-x64": ("darwin", "amd64")}


def checksum(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", help="v-prefixed release tag")
    parser.add_argument("--out", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[a-zA-Z0-9.]+)?", args.version):
        parser.error("expected vMAJOR.MINOR.PATCH[-prerelease]")
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("output directory must be empty")
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    epoch = int(subprocess.check_output(["git", "show", "-s", "--format=%ct", "HEAD"], cwd=ROOT))
    goversion = subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip()
    notice = out / "notices.tmp"
    subprocess.run(["bash", str(ROOT / "scripts/third_party_notices.sh"), str(notice)], check=True, cwd=ROOT)
    installer = (ROOT / "packaging/install.sh").read_text().replace("release='@VERSION@'", f"release='{args.version}'")
    assets = []
    for host, (goos, goarch) in HOSTS.items():
        stage = out / host
        stage.mkdir()
        binary = "slepok.exe" if goos == "windows" else "slepok"
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags",
                        f"-s -w -X main.version={args.version[1:]}", "-o", str(stage / binary), "./cmd/slepok"],
                       cwd=ROOT, env={**os.environ, "CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch}, check=True)
        for source, target in [(ROOT / "LICENSE", "LICENSE"), (notice, "THIRD_PARTY_NOTICES.md"),
                               (ROOT / "docs/INSTALL.md", "INSTALL.md")]:
            (stage / target).write_bytes(source.read_bytes())
        (stage / "release.json").write_text(json.dumps({"version": args.version, "commit": commit,
             "go": goversion, "platform": host}, indent=2) + "\n")
        if goos != "windows":
            (stage / "install.sh").write_text(installer)
        (stage / "SHA256SUMS").write_text("".join(f"{checksum(p)}  {p.name}\n" for p in sorted(stage.iterdir())))
        stem = f"slepok-{args.version[1:]}-{host}"
        archive = out / (stem + (".zip" if goos == "windows" else ".tar.gz"))
        if goos == "windows":
            with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as zf:
                for p in sorted(stage.iterdir()):
                    info = zipfile.ZipInfo(p.name, (2026, 1, 1, 0, 0, 0))
                    info.compress_type = zipfile.ZIP_DEFLATED
                    info.external_attr = 0o100644 << 16
                    zf.writestr(info, p.read_bytes())
        else:
            with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=epoch) as gz:
                with tarfile.open(fileobj=gz, mode="w") as tf:
                    for p in sorted(stage.iterdir()):
                        data = p.read_bytes()
                        info = tarfile.TarInfo(p.name)
                        info.size, info.mtime = len(data), epoch
                        info.mode = 0o755 if p.name in ("slepok", "install.sh") else 0o644
                        tf.addfile(info, io.BytesIO(data))
        assets.append(archive)
    notice.unlink()
    bootstrap = out / "install.sh"
    bootstrap.write_text(installer)
    assets.append(bootstrap)
    (out / "SHA256SUMS").write_text("".join(f"{checksum(p)}  {p.name}\n" for p in assets))


if __name__ == "__main__":
    main()
