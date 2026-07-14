#!/usr/bin/env python3
"""Prevent exact Phase 50/60 helper duplication from returning.

The control-pair library is the single owner for helpers whose implementation is
identical in both phases. Phase-specific helpers may intentionally differ in
mode handling, logging, and handoff policy, so this validator only rejects
byte-for-byte duplicates.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
PHASE_50 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-50/run-phase50.sh"
PHASE_60 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-60/run-phase60.sh"
FUNCTION_START = re.compile(
    r"^(?:(?:function)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{", re.MULTILINE
)


def functions(path: Path) -> dict[str, str]:
    """Return shell-function source keyed by name.

    Adaetum's bootstrap functions use balanced braces. This small parser also
    ignores braces inside quoted strings, which is enough for this structural
    repository check without executing any shell code.
    """
    source = path.read_text(encoding="utf-8")
    result: dict[str, str] = {}
    for match in FUNCTION_START.finditer(source):
        depth = 0
        quote: str | None = None
        index = match.end() - 1
        while index < len(source):
            character = source[index]
            line_start = source.rfind("\n", 0, index) + 1
            if (
                quote is None
                and character == "#"
                and source[line_start:index].strip() == ""
            ):
                next_line = source.find("\n", index)
                index = len(source) if next_line == -1 else next_line
                continue
            if quote:
                if character == "\\":
                    index += 1
                elif character == quote:
                    quote = None
            elif character in "'\"":
                quote = character
            elif character == "{":
                depth += 1
            elif character == "}":
                depth -= 1
                if depth == 0:
                    result[match.group(1)] = source[match.start() : index + 1]
                    break
            index += 1
        else:
            raise ValueError(f"{path}: could not find the end of {match.group(1)}")
    return result


def main() -> int:
    phase_50_functions = functions(PHASE_50)
    phase_60_functions = functions(PHASE_60)
    duplicates = sorted(
        name
        for name, implementation in phase_50_functions.items()
        if phase_60_functions.get(name) == implementation
    )
    if duplicates:
        print("Move exact shared helpers into control-pair-common.sh:", file=sys.stderr)
        print("\n".join(f"- {name}" for name in duplicates), file=sys.stderr)
        return 1
    print(
        "Control-pair helper ownership ok: "
        f"{len(phase_50_functions)} Phase 50 and {len(phase_60_functions)} Phase 60 helpers."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
