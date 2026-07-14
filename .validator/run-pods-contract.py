#!/usr/bin/env python3
"""Run the complete local contract suite for generated pod configuration."""
from __future__ import annotations

import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]

COMMANDS = [
    ("rendered pods config", [sys.executable, "tasks/scripts/render-pods-config.py", "--check"]),
    ("public-safe pods templates", [sys.executable, ".validator/validate-pods-public-safe.py"]),
    ("cluster config values", [sys.executable, ".validator/validate-cluster-config.py"]),
    ("pod app definitions", [sys.executable, ".validator/validate-pod-app-definitions.py"]),
    ("pods kustomize builds", [sys.executable, ".validator/validate-pods-kustomize.py"]),
    ("pods template inventory", [sys.executable, ".validator/validate-pods-template-inventory.py"]),
    ("setup pods roundtrip", [sys.executable, ".validator/validate-setup-pods-roundtrip.py"]),
    ("ApplicationSet smoke", [sys.executable, ".validator/validate-applicationset-smoke.py"]),
    ("pods docs contract", [sys.executable, ".validator/validate-pods-docs.py"]),
    ("pods secret patterns", [sys.executable, ".validator/validate-pods-secrets.py"]),
    ("pods consistency", [sys.executable, ".validator/validate-pods-consistency.py"]),
]


def main() -> int:
    failures = 0
    for label, command in COMMANDS:
        print(f"[pods-contract] {label}")
        result = subprocess.run(command, cwd=REPO_ROOT, text=True, capture_output=True, check=False)
        if result.returncode != 0:
            failures += 1
            if result.stdout:
                sys.stdout.write(result.stdout)
            if result.stderr:
                sys.stderr.write(result.stderr)
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
