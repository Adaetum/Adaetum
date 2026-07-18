#!/usr/bin/env python3
"""Check that repository-local Markdown links resolve to tracked paths.

External links are intentionally out of scope: validating them would make local
checks depend on network availability. This validator only checks the stable
links Adaetum owns inside the repository.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlparse


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
MARKDOWN_LINK = re.compile(r"(?<!!)\[[^\]]*\]\(([^)]+)\)")
IGNORED_SCHEMES = {"http", "https", "mailto", "tel"}
IGNORED_DIRECTORIES = {".git", ".venv", "node_modules"}


def markdown_files() -> list[Path]:
    return [
        path
        for path in REPOSITORY_ROOT.rglob("*.md")
        if not any(part in IGNORED_DIRECTORIES for part in path.parts)
    ]


def local_target(source: Path, raw_target: str) -> Path | None:
    """Return a local link target, or None when the link is intentionally external."""
    target = raw_target.strip().strip("<>")
    parsed = urlparse(target)
    if parsed.scheme in IGNORED_SCHEMES or parsed.netloc or target.startswith("#"):
        return None

    path_text = unquote(parsed.path)
    if not path_text:
        return None
    return (source.parent / path_text).resolve()


def validate_file(path: Path) -> list[str]:
    failures: list[str] = []
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        for match in MARKDOWN_LINK.finditer(line):
            target = local_target(path, match.group(1))
            if target is not None and not target.exists():
                relative_source = path.relative_to(REPOSITORY_ROOT)
                failures.append(f"{relative_source}:{line_number}: missing local link: {match.group(1)}")
    return failures


def main() -> int:
    failures = [failure for path in markdown_files() for failure in validate_file(path)]
    if failures:
        print("Markdown link validation failed:", file=sys.stderr)
        print("\n".join(failures), file=sys.stderr)
        return 1
    print(f"Markdown links ok: {len(markdown_files())} files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
