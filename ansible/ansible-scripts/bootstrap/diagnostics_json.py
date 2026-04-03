#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

FAILURE_KINDS = {
    "network",
    "dns",
    "http",
    "kubernetes-api",
    "rollout",
    "chart-fetch",
    "image-pull",
    "auth",
    "secret",
    "timeout",
    "config",
    "unknown",
}


def iso_timestamp() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def excerpt(value: Any, limit: int = 1200) -> str:
    text = str(value or "").replace("\r", "").strip()
    if len(text) <= limit:
        return text
    return text[:limit].rstrip() + "...<truncated>"


def classify_failure_kind(text: str, *, http_status: str = "") -> str:
    haystack = f"{text}\n{http_status}".lower()
    if not haystack.strip():
        return "unknown"
    if "image pull" in haystack or "imagepullbackoff" in haystack or "errimagepull" in haystack:
        return "image-pull"
    if "release-assets.githubusercontent.com" in haystack or "failed to fetch" in haystack or "helm repo" in haystack:
        return "chart-fetch"
    if "timed out" in haystack or "deadline exceeded" in haystack or "timeout" in haystack:
        return "timeout"
    if "no such host" in haystack or "nxdomain" in haystack or "does not resolve" in haystack:
        return "dns"
    if "http error" in haystack or re.search(r"\bhttp[=/ :_-]*[45]\d\d\b", haystack):
        return "http"
    if "forbidden" in haystack or "unauthorized" in haystack or "authentication failed" in haystack:
        return "auth"
    if "secret" in haystack and ("missing" in haystack or "empty" in haystack or "incomplete" in haystack):
        return "secret"
    if "rollout" in haystack or "deployment did not become ready" in haystack:
        return "rollout"
    if "kubectl" in haystack or "no matches for kind" in haystack or "the server doesn't have a resource type" in haystack:
        return "kubernetes-api"
    if "connection refused" in haystack or "tls handshake timeout" in haystack or "connection reset" in haystack:
        return "network"
    if "invalid" in haystack or "missing" in haystack or "not found" in haystack or "unsupported" in haystack:
        return "config"
    return "unknown"


def normalize_record(record: dict[str, Any]) -> dict[str, Any]:
    data = dict(record)
    if not data.get("timestamp"):
        data["timestamp"] = iso_timestamp()

    if "evidence_paths" in data and isinstance(data["evidence_paths"], str):
        raw = data["evidence_paths"].strip()
        if raw:
            data["evidence_paths"] = [part for part in raw.split(os.pathsep) if part]
        else:
            data["evidence_paths"] = []

    for key in ("stdout_excerpt", "stderr_excerpt", "summary"):
        if key in data:
            data[key] = excerpt(data.get(key, ""))

    failure_kind = str(data.get("failure_kind") or "").strip()
    if failure_kind not in FAILURE_KINDS:
        joined = "\n".join(
            str(data.get(field) or "")
            for field in ("summary", "stderr_excerpt", "stdout_excerpt")
        )
        data["failure_kind"] = classify_failure_kind(joined, http_status=str(data.get("http_status") or ""))

    return data


def append_record(output_path: str, record: dict[str, Any]) -> dict[str, Any]:
    normalized = normalize_record(record)
    path = Path(output_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(normalized, sort_keys=True) + "\n")
    return normalized


def parse_fields(values: list[str]) -> dict[str, Any]:
    record: dict[str, Any] = {}
    for item in values:
        if "=" not in item:
            raise SystemExit(f"invalid --field {item!r}, expected key=value")
        key, value = item.split("=", 1)
        record[key] = value
    return record


def main() -> int:
    parser = argparse.ArgumentParser(description="Append one structured diagnostics record to a JSONL file.")
    parser.add_argument("--output", required=True)
    parser.add_argument("--field", action="append", default=[])
    parser.add_argument("--record-json")
    args = parser.parse_args()

    record: dict[str, Any] = {}
    if args.record_json:
        record.update(json.loads(args.record_json))
    record.update(parse_fields(args.field))
    append_record(args.output, record)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
