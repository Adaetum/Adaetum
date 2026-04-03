#!/usr/bin/env python3
from __future__ import annotations

import shutil
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
KUSTOMIZATION_DIRS = sorted({path.parent for path in (REPO_ROOT / "pods").rglob("kustomization.yaml")})
LOAD_RESTRICTOR_EXCEPTIONS = {
    REPO_ROOT / "pods" / "secrets" / "openbao" / "config",
}


def main() -> int:
    kubectl = shutil.which("kubectl")
    if not kubectl:
        print("kubectl is required to validate pods kustomize directories", file=sys.stderr)
        return 1

    failures: list[str] = []
    for directory in KUSTOMIZATION_DIRS:
        command = [kubectl, "kustomize", str(directory)]
        if directory in LOAD_RESTRICTOR_EXCEPTIONS:
            command.append("--load-restrictor=LoadRestrictionsNone")
        result = subprocess.run(
            command,
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            rel = directory.relative_to(REPO_ROOT)
            stderr = (result.stderr or result.stdout or "").strip()
            failures.append(f"{rel}: kubectl kustomize failed: {stderr}")

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
