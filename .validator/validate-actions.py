#!/usr/bin/env python3
"""Validate GitHub Actions workflow YAML files.

Checks:
- YAML parses cleanly
- Top-level document is a mapping
- Required keys are present: `name` and `on`
- Every job honors the upstream-repository Actions disable variable
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml


DISABLE_ACTIONS_CONDITION = "vars.ADAETUM_DISABLE_ACTIONS != 'true'"


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

    jobs = doc.get("jobs")
    if not isinstance(jobs, dict) or not jobs:
        errors.append(f"{path}: missing required top-level jobs mapping")
        return errors

    for job_name, job in jobs.items():
        if not isinstance(job, dict):
            errors.append(f"{path}: job {job_name!r} must be a mapping/object")
            continue
        condition = job.get("if")
        if not isinstance(condition, str) or DISABLE_ACTIONS_CONDITION not in condition:
            errors.append(
                f"{path}: job {job_name!r} must honor {DISABLE_ACTIONS_CONDITION}"
            )

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
