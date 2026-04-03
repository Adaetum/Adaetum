#!/usr/bin/env python3
"""Lightweight kickstart validator.

Checks:
- file is readable and non-empty
- required kickstart sections/markers exist
"""

from __future__ import annotations

import sys
from pathlib import Path

REQUIRED_SNIPPETS = (
    "%packages",
    "%post",
)


def validate_file(path: Path) -> list[str]:
    errors: list[str] = []
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        return [f"{path}: unable to read file: {exc}"]

    if not text.strip():
        return [f"{path}: file is empty"]

    for marker in REQUIRED_SNIPPETS:
        if marker not in text:
            errors.append(f"{path}: missing required marker: {marker}")

    return errors


def main(argv: list[str]) -> int:
    paths = [Path(p) for p in argv[1:] if p]
    if not paths:
        return 0

    failures: list[str] = []
    for path in paths:
        failures.extend(validate_file(path))

    if failures:
        for line in failures:
            print(line, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
