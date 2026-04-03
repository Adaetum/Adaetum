#!/usr/bin/env python3
from __future__ import annotations

import re
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
SCAN_PATHS = [REPO_ROOT / "pods", REPO_ROOT / "setup.md"]

ALLOWED_VALUES = {
    "",
    "change-me",
    "CHANGEME",
    "example",
    "example.local",
    "example.services",
    "example.ts.net",
    "REDACTED",
    "[redacted]",
    "your-token-here",
    "your-secret-here",
}

KEY_ASSIGNMENT_RE = re.compile(
    r"(?i)\b([A-Z0-9_]*(?:token|password|authkey|client_secret|private[_-]?key|secret_key)[A-Z0-9_]*)\b\s*[:=]\s*[\"']?([^\"'\s#]+)"
)

BEARER_RE = re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._=-]{20,}\b")
GITHUB_PAT_RE = re.compile(r"\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b")
PRIVATE_KEY_RE = re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")
BASE64_BLOB_RE = re.compile(r"\b[A-Za-z0-9+/]{80,}={0,2}\b")


def iter_files() -> list[Path]:
    files: list[Path] = []
    for path in SCAN_PATHS:
        if path.is_file():
            files.append(path)
            continue
        files.extend(
            candidate
            for candidate in path.rglob("*")
            if candidate.is_file() and candidate.suffix.lower() in {".yaml", ".yml", ".env", ".md", ".tmpl"}
        )
    return sorted(files)


def is_placeholder(value: str) -> bool:
    lowered = value.strip().lower()
    if lowered in {entry.lower() for entry in ALLOWED_VALUES}:
        return True
    if lowered.startswith("example.") or lowered.startswith("your-") or lowered.startswith("${"):
        return True
    if value.strip().startswith("__") and value.strip().endswith("__"):
        return True
    if lowered.startswith("<") and lowered.endswith(">"):
        return True
    if lowered.startswith("`") and lowered.endswith("`"):
        return True
    if lowered.startswith("$("):
        return True
    if lowered in {"true", "false"}:
        return True
    return False


def validate_file(path: Path) -> list[str]:
    failures: list[str] = []
    rel = path.relative_to(REPO_ROOT)
    text = path.read_text(encoding="utf-8")

    if PRIVATE_KEY_RE.search(text):
        failures.append(f"{rel}: contains private key material marker")
    if GITHUB_PAT_RE.search(text):
        failures.append(f"{rel}: contains GitHub token-like value")
    if BEARER_RE.search(text):
        failures.append(f"{rel}: contains bearer token-like value")

    for lineno, line in enumerate(text.splitlines(), start=1):
        if "secretKeyRef" in line or "valueFrom:" in line:
            continue
        assignment = KEY_ASSIGNMENT_RE.search(line)
        if assignment:
            key, value = assignment.groups()
            if key.lower().endswith("passwordmode"):
                continue
            if not is_placeholder(value):
                failures.append(f"{rel}:{lineno}: suspicious secret-like assignment value: {value}")

        if BASE64_BLOB_RE.search(line) and not is_placeholder(line.strip()):
            failures.append(f"{rel}:{lineno}: contains unusually long base64-like blob")

    return failures


def main() -> int:
    failures: list[str] = []
    for path in iter_files():
        failures.extend(validate_file(path))

    if failures:
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
