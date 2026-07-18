#!/usr/bin/env python3
"""Remove rebuildable outputs while preserving repository and secret inputs."""
from __future__ import annotations

import argparse
import shutil
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DIST_DIR = REPO_ROOT / "dist"

REMOVE_PATHS = (
    DIST_DIR / "bootstrap-runtime.env",
    DIST_DIR / "ansible-runner-bundle.tar.gz",
    DIST_DIR / ".live-ansible-runner-bundle.tar.gz",
    DIST_DIR / "ks-templates",
    DIST_DIR / "iso-work",
)

REMOVE_GLOBS = (
    "dist/*-ks.iso",
)


def collect_targets() -> list[Path]:
    targets: list[Path] = []
    seen: set[Path] = set()

    for path in REMOVE_PATHS:
        if path.exists() and path not in seen:
            seen.add(path)
            targets.append(path)

    for pattern in REMOVE_GLOBS:
        for path in REPO_ROOT.glob(pattern):
            if path.exists() and path not in seen:
                seen.add(path)
                targets.append(path)

    return sorted(targets)


def remove_path(path: Path) -> None:
    if path.is_dir():
        shutil.rmtree(path)
    else:
        path.unlink()


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Remove derived setup/build artifacts without touching durable local setup inputs."
    )
    parser.add_argument("--preview", action="store_true", help="Print paths that would be removed.")
    args = parser.parse_args(argv[1:])

    targets = collect_targets()
    if args.preview:
        for path in targets:
            print(path.relative_to(REPO_ROOT))
        return 0

    for path in targets:
        remove_path(path)

    print("Removed derived setup/build artifacts from dist/.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
