#!/usr/bin/env python3
"""Derive a content-based tag for Adaetum's locally built runner image."""
from __future__ import annotations

import hashlib
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
BUILD_INPUTS = (
    REPO_ROOT / "ansible" / "Dockerfile",
    REPO_ROOT / "ansible" / "ansible-scripts",
)


def image_tag() -> str:
    """Hash every Docker build input copied into the runner image."""
    digest = hashlib.sha256()
    files: list[Path] = []
    for build_input in BUILD_INPUTS:
        if build_input.is_dir():
            files.extend(path for path in build_input.rglob("*") if path.is_file())
        elif build_input.is_file():
            files.append(build_input)
        else:
            raise FileNotFoundError(f"missing ansible-runner build input: {build_input}")

    for path in sorted(files, key=lambda item: item.relative_to(REPO_ROOT).as_posix()):
        relative = path.relative_to(REPO_ROOT).as_posix().encode()
        content = path.read_bytes()
        digest.update(len(relative).to_bytes(4, "big"))
        digest.update(relative)
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
    return f"build-{digest.hexdigest()[:16]}"


if __name__ == "__main__":
    print(image_tag())
