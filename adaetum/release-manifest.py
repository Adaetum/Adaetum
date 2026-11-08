#!/usr/bin/env python3
"""Record immutable artifacts from one Adaetum agent release."""
import hashlib
import json
import os
import re
import sys
from pathlib import Path


def main() -> None:
    directory = Path(sys.argv[1])
    version = os.environ["ADAETUM_VERSION"]
    tag = os.environ["ADAETUM_RELEASE_TAG"]
    repository = os.environ["GITHUB_REPOSITORY"]
    source = os.environ["GITHUB_SHA"]
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-beta\.(0|[1-9][0-9]*))?", version) or tag != f"adaetum-v{version}":
        raise ValueError("invalid release version or tag")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("invalid release repository")
    if not re.fullmatch(r"[0-9a-f]{40}", source):
        raise ValueError("invalid source commit")
    registry = "ghcr.io/" + repository.split("/", 1)[0].lower()
    images = {}
    for component in ("agent", "auth", "embed"):
        image = (directory / f"{component}.image").read_text().strip()
        expected = re.escape(f"{registry}/adaetum-{component}") + r"@sha256:[0-9a-f]{64}"
        if not re.fullmatch(expected, image):
            raise ValueError(f"invalid pinned {component} image")
        images[component] = image
    binaries = {}
    for component in ("agent", "auth"):
        for arch in ("amd64", "arm64"):
            name = f"adaetum-{component}-linux-{arch}"
            if (directory / name).stat().st_size == 0:
                raise ValueError(f"empty release binary: {name}")
            with (directory / name).open("rb") as artifact:
                hasher = hashlib.sha256()
                for chunk in iter(lambda: artifact.read(1024 * 1024), b""):
                    hasher.update(chunk)
                digest = hasher.hexdigest()
            binaries[name] = {
                "url": f"https://github.com/{repository}/releases/download/{tag}/{name}",
                "sha256": digest,
            }
    manifest = {"version": version, "source_commit": source, "images": images, "binaries": binaries}
    (directory / "release.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
