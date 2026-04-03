#!/usr/bin/env python3

import json
import re
import sys
from typing import Any


def _strip_hujson(text: str) -> str:
    """
    Best-effort conversion of Tailscale's HuJSON policy file to strict JSON.

    This is intentionally minimal: it removes line/block comments and strips
    trailing commas. It is sufficient for typical ACL policy files.
    """
    # Remove /* ... */ comments
    text = re.sub(r"/\\*.*?\\*/", "", text, flags=re.S)
    # Remove // ... comments
    text = re.sub(r"(^|[^:])//.*?$", r"\\1", text, flags=re.M)
    # Remove trailing commas before } or ]
    text = re.sub(r",\\s*([}\\]])", r"\\1", text)
    return text


def _parse_policy(text: str) -> Any:
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return json.loads(_strip_hujson(text))


def _read_policy_from_input(raw: str) -> str:
    """
    Accept either:
    - raw policy text (hujson/json)
    - a JSON wrapper with a 'policy' field containing the policy text
    """
    raw = raw.strip()
    if not raw:
        return ""
    try:
        wrapper = json.loads(raw)
    except json.JSONDecodeError:
        return raw
    if isinstance(wrapper, dict) and isinstance(wrapper.get("policy"), str):
        return wrapper["policy"]
    return raw


def main() -> int:
    group_key = sys.argv[1] if len(sys.argv) > 1 else ""
    if not group_key:
        print("usage: extract_acl_group_members.py <group-key>", file=sys.stderr)
        return 2

    raw_in = sys.stdin.read()
    policy_text = _read_policy_from_input(raw_in)
    if not policy_text:
        print("[]")
        return 0

    policy = _parse_policy(policy_text)
    groups = {}
    if isinstance(policy, dict):
        groups = policy.get("groups") or {}

    if not isinstance(groups, dict):
        print("[]")
        return 0

    # Allow either "group:local-admin" or "local-admin" for convenience.
    candidates = [group_key]
    if not group_key.startswith("group:"):
        candidates.append(f"group:{group_key}")
    else:
        candidates.append(group_key.split("group:", 1)[1])

    members = None
    for key in candidates:
        if key in groups:
            members = groups[key]
            break

    if not members:
        print("[]")
        return 0

    if not isinstance(members, list):
        print("[]")
        return 0

    out = [m for m in members if isinstance(m, str) and m.strip()]
    print(json.dumps(out))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

