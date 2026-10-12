#!/usr/bin/env python3
"""Build versioned Core assets with the filenames expected by Tauri."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

TARGETS = {
    "aarch64-apple-darwin": ("darwin", "arm64"),
    "x86_64-apple-darwin": ("darwin", "amd64"),
    "aarch64-unknown-linux-gnu": ("linux", "arm64"),
    "x86_64-unknown-linux-gnu": ("linux", "amd64"),
    "aarch64-pc-windows-msvc": ("windows", "arm64"),
    "x86_64-pc-windows-msvc": ("windows", "amd64"),
}
VERSION = re.compile(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?\Z")
ROOT = Path(__file__).resolve().parent.parent


def output(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="Exact release tag, e.g. v0.1.0")
    parser.add_argument("--output", default="dist/core")
    parser.add_argument("--target", action="append", choices=TARGETS)
    parser.add_argument("--allow-dirty", action="store_true", help="Local validation only")
    args = parser.parse_args()
    if not VERSION.fullmatch(args.version):
        parser.error("version must be a vX.Y.Z tag, optionally with a prerelease suffix")
    dirty = bool(output("git", "status", "--porcelain"))
    if dirty and not args.allow_dirty:
        parser.error("release builds require a clean checkout")
    destination = Path(args.output).resolve()
    if destination.exists():
        parser.error(f"output already exists: {destination}; choose a fresh directory")
    destination.parent.mkdir(parents=True, exist_ok=True)
    manifest = {
        "schemaVersion": 1,
        "repository": os.environ.get("GITHUB_REPOSITORY", "nite0x/traio"),
        "version": args.version,
        "commit": output("git", "rev-parse", "HEAD"),
        "sourceDirty": dirty,
        "goVersion": output("go", "version"),
        "binaries": [],
    }
    # Publish the directory only after every requested target built successfully.
    with tempfile.TemporaryDirectory(dir=destination.parent, prefix=".core-release-") as tmp:
        staging = Path(tmp) / "assets"
        staging.mkdir()
        for target in dict.fromkeys(args.target or TARGETS):
            goos, goarch = TARGETS[target]
            filename = f"traio-server-{target}" + (".exe" if goos == "windows" else "")
            binary = staging / filename
            print(f"Building {target}", flush=True)
            subprocess.run(
                ["go", "build", "-trimpath", "-buildvcs=true", "-ldflags=-s -w",
                 "-o", str(binary), "./cmd/server"],
                cwd=ROOT,
                env={**os.environ, "CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch},
                check=True,
            )
            manifest["binaries"].append({
                "target": target, "filename": filename, "goos": goos, "goarch": goarch,
                "size": binary.stat().st_size,
                "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
            })
        (staging / "core-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        (staging / "SHA256SUMS").write_text("".join(
            f"{entry['sha256']}  {entry['filename']}\n" for entry in manifest["binaries"]
        ))
        staging.rename(destination)
    print(f"Core release assets: {destination}")


if __name__ == "__main__":
    main()
