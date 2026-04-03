#!/usr/bin/env python3
from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
LOCAL_OVERRIDE_PATH = REPO_ROOT / ".maintainer-overrides" / "allow-example-placeholders"
GIT_DIR_OVERRIDE_PATH = REPO_ROOT / ".git" / "adaetum-allow-example-placeholders"
PLACEHOLDER_RE = re.compile(r"(?<![A-Za-z0-9_])example\.[A-Za-z0-9._-]+")
ALLOWED_EXAMPLE_TOKENS = {
    "example.com",
}

TEXT_TARGETS = (
    REPO_ROOT / "pods" / "cluster-config" / "cluster-config.env",
    REPO_ROOT / "dist" / "bootstrap-runtime.env",
)

GLOB_TARGETS = (
    "dist/ks-templates/*.ks",
)

KUSTOMIZE_TARGETS = (
    REPO_ROOT / "pods" / "ingress",
    REPO_ROOT / "pods" / "portal" / "homepage",
    REPO_ROOT / "pods" / "ansible" / "ansible",
)


def allow_example_placeholders() -> bool:
    if os.environ.get("ALLOW_EXAMPLE_PLACEHOLDERS", "").strip().lower() in {"1", "true", "yes", "on"}:
        return True
    if LOCAL_OVERRIDE_PATH.exists():
        return True
    if GIT_DIR_OVERRIDE_PATH.exists():
        return True
    result = subprocess.run(
        ["git", "config", "--bool", "--get", "adaetum.allowExamplePlaceholders"],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode == 0 and result.stdout.strip().lower() == "true":
        return True
    return False


def iter_text_targets() -> list[Path]:
    files: list[Path] = []
    seen: set[Path] = set()

    for path in TEXT_TARGETS:
        if path.is_file() and path not in seen:
            seen.add(path)
            files.append(path)

    for pattern in GLOB_TARGETS:
        for path in REPO_ROOT.glob(pattern):
            if path.is_file() and path not in seen:
                seen.add(path)
                files.append(path)

    return sorted(files)


def find_failures_in_lines(label: str, lines: list[str]) -> list[str]:
    failures: list[str] = []
    for index, raw_line in enumerate(lines, start=1):
        stripped = raw_line.lstrip()
        if not stripped or stripped.startswith("#"):
            continue
        candidate = raw_line.split("#", 1)[0]
        matches = [match.group(0) for match in PLACEHOLDER_RE.finditer(candidate)]
        unresolved = [match for match in matches if match not in ALLOWED_EXAMPLE_TOKENS]
        if unresolved:
            failures.append(f"{label}:{index}: unresolved example placeholder: {raw_line.strip()}")
    return failures


def find_failures_in_file(path: Path) -> list[str]:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except FileNotFoundError:
        return []
    return find_failures_in_lines(str(path.relative_to(REPO_ROOT)), lines)


def render_kustomize(directory: Path) -> tuple[list[str], list[str]]:
    kubectl = shutil.which("kubectl")
    if not kubectl:
        return [], ["kubectl is required to validate rendered example placeholders"]

    result = subprocess.run(
        [kubectl, "kustomize", str(directory)],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        stderr = (result.stderr or result.stdout or "").strip()
        return [], [f"{directory.relative_to(REPO_ROOT)}: kubectl kustomize failed: {stderr}"]
    return result.stdout.splitlines(), []


def main() -> int:
    if allow_example_placeholders():
        return 0

    failures: list[str] = []

    for path in iter_text_targets():
        failures.extend(find_failures_in_file(path))

    for directory in KUSTOMIZE_TARGETS:
        lines, render_failures = render_kustomize(directory)
        failures.extend(render_failures)
        if render_failures:
            continue
        failures.extend(find_failures_in_lines(f"{directory.relative_to(REPO_ROOT)} (rendered)", lines))

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
