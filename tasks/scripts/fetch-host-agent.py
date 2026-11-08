#!/usr/bin/env python3
"""Fetch the pinned host agent for inclusion on installer media."""
import argparse
import hashlib
import json
import os
import re
import tempfile
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
LOCK = ROOT / "adaetum" / "host-agent-release.json"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", required=True, choices=("amd64", "arm64"))
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    lock = json.loads(LOCK.read_text(encoding="utf-8"))
    version, expected = lock["version"], lock["sha256"][args.arch]
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-beta\.(0|[1-9][0-9]*))?", version):
        raise ValueError("invalid host agent release version")
    if not re.fullmatch(r"[0-9a-f]{64}", expected):
        raise ValueError("invalid host agent checksum")
    url = f"https://github.com/Adaetum/Adaetum/releases/download/adaetum-v{version}/adaetum-agent-linux-{args.arch}"
    args.output.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=".adaetum-agent-", dir=args.output.parent)
    try:
        digest = hashlib.sha256()
        with os.fdopen(fd, "wb") as output, urllib.request.urlopen(url, timeout=120) as response:
            for block in iter(lambda: response.read(1024 * 1024), b""):
                digest.update(block)
                output.write(block)
        if digest.hexdigest() != expected:
            raise ValueError("host agent checksum mismatch")
        os.chmod(name, 0o755)
        os.replace(name, args.output)
    finally:
        if os.path.exists(name):
            os.unlink(name)


if __name__ == "__main__":
    main()
