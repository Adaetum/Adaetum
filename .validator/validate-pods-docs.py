#!/usr/bin/env python3
"""Keep setup documentation aligned with the supported pods configuration contract."""
from __future__ import annotations

import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]

FORBIDDEN_DOC_STRINGS = {
    "setup.md": (
        "update the hosts in `pods/ingress/nginx-routing/*.yaml`",
        "update the hosts in `pods/ingress/observability-routing/*.yaml`",
    ),
}

REQUIRED_DOC_STRINGS = {
    "setup.md": (
        "`pods/cluster-config/cluster-config.env`",
        "re-renders the Argo/bootstrap files",
    ),
}


def main() -> int:
    failures: list[str] = []
    for rel_path, forbidden_values in FORBIDDEN_DOC_STRINGS.items():
        text = (REPO_ROOT / rel_path).read_text(encoding="utf-8")
        for value in forbidden_values:
            if value in text:
                failures.append(f"{rel_path}: forbidden outdated guidance present: {value}")

    for rel_path, required_values in REQUIRED_DOC_STRINGS.items():
        text = (REPO_ROOT / rel_path).read_text(encoding="utf-8")
        for value in required_values:
            if value not in text:
                failures.append(f"{rel_path}: missing required guidance: {value}")

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
