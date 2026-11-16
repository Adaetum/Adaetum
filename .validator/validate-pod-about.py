#!/usr/bin/env python3
"""Every pods/**/<app>.about.yaml must be a file the agent can show.

The agent (adaetum/agent/internal/about) rejects a malformed description and shows the app's
page a "not valid" notice instead, so a bad file merged here is a visible defect for operators.
This applies the same shape and limits before merge, and requires the file to sit next to the
<app>.app.yaml it describes (the agent finds it by that app name). Keep the limits in step with
internal/about/about.go.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parents[1]
LIMITS = {"summary": 400, "label": 40, "value": 300, "watch": 200}
MAX_FACTS, MAX_WATCH, MAX_BYTES = 8, 5, 16 << 10
TOP_LEVEL = {"summary", "facts", "watch", "written"}


class TextDates(yaml.SafeLoader):
    """Reads dates as the text the agent sees, not as date objects."""


TextDates.yaml_implicit_resolvers = {
    key: [item for item in resolvers if item[0] != "tag:yaml.org,2002:timestamp"]
    for key, resolvers in yaml.SafeLoader.yaml_implicit_resolvers.items()
}


def text(failures: list[str], where: str, value: object, limit: int, required: bool = True) -> None:
    if not isinstance(value, str) or (required and not value.strip()):
        failures.append(f"{where}: must be non-empty text")
    elif len(value.strip()) > limit:
        failures.append(f"{where}: longer than {limit} characters")
    elif "<" in value or ">" in value:
        failures.append(f"{where}: contains markup")


def check(path: Path) -> list[str]:
    rel = path.relative_to(REPO_ROOT)
    failures: list[str] = []
    if path.stat().st_size > MAX_BYTES:
        return [f"{rel}: larger than {MAX_BYTES} bytes"]
    app = path.name[: -len(".about.yaml")]
    if not (path.parent / f"{app}.app.yaml").is_file():
        failures.append(f"{rel}: no {app}.app.yaml beside it")
    data = yaml.load(path.read_text(encoding="utf-8"), Loader=TextDates)
    if not isinstance(data, dict):
        return failures + [f"{rel}: must be a mapping"]
    for extra in sorted(set(data) - TOP_LEVEL):
        failures.append(f"{rel}: unknown field {extra!r}")
    text(failures, f"{rel} summary", data.get("summary"), LIMITS["summary"])
    facts = data.get("facts") or []
    if not isinstance(facts, list) or len(facts) > MAX_FACTS:
        failures.append(f"{rel}: facts must be a list of at most {MAX_FACTS}")
    else:
        for fact in facts:
            if not isinstance(fact, dict) or set(fact) != {"label", "value"}:
                failures.append(f"{rel}: each fact needs exactly a label and a value")
                continue
            text(failures, f"{rel} fact label", fact["label"], LIMITS["label"])
            text(failures, f"{rel} fact value", fact["value"], LIMITS["value"])
    watch = data.get("watch") or []
    if not isinstance(watch, list) or len(watch) > MAX_WATCH:
        failures.append(f"{rel}: watch must be a list of at most {MAX_WATCH}")
    else:
        for item in watch:
            text(failures, f"{rel} watch item", item, LIMITS["watch"])
    written = data.get("written")
    if not isinstance(written, dict) or set(written) - {"by", "date", "version"}:
        failures.append(f"{rel}: written needs by, date and optionally version")
    else:
        if written.get("by") not in ("person", "assistant"):
            failures.append(f'{rel}: written.by must be "person" or "assistant"')
        if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", str(written.get("date", ""))):
            failures.append(f"{rel}: written.date must be a date, YYYY-MM-DD")
        if "version" in written:
            text(failures, f"{rel} written.version", str(written["version"]), 60, required=False)
    return failures


def main() -> int:
    failures: list[str] = []
    for path in sorted((REPO_ROOT / "pods").rglob("*.about.yaml")):
        failures.extend(check(path))
    if failures:
        print("About files the agent could not show:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
