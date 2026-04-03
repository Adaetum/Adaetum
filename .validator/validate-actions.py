#!/usr/bin/env python3
"""Validate GitHub Actions workflow YAML files.

Checks:
- YAML parses cleanly
- Top-level document is a mapping
- Required keys are present: `name` and `on`
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml


def validate_file(path: Path) -> list[str]:
    errors: list[str] = []
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        return [f"{path}: unable to read file: {exc}"]

    try:
        doc = yaml.safe_load(text)
    except yaml.YAMLError as exc:
        return [f"{path}: YAML parse error: {exc}"]

    if not isinstance(doc, dict):
        return [f"{path}: top-level YAML must be a mapping/object"]

    if "name" not in doc:
        errors.append(f"{path}: missing required top-level key: name")

    # PyYAML may parse YAML 1.1 plain key `on` as boolean True.
    if "on" not in doc and True not in doc:
        errors.append(f"{path}: missing required top-level key: on")

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
