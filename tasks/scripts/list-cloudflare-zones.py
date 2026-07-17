#!/usr/bin/env python3
"""List active Cloudflare zones without displaying the API token."""
from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request


def zones_from_api(token: str) -> list[tuple[str, str, str]]:
    request = urllib.request.Request("https://api.cloudflare.com/client/v4/zones?status=active&per_page=50")
    request.add_header("Authorization", f"Bearer {token}")
    request.add_header("User-Agent", "adaetum-first-run")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            document = json.load(response)
    except (OSError, urllib.error.HTTPError, json.JSONDecodeError) as exc:
        raise ValueError(f"unable to list Cloudflare zones: {exc}") from exc
    if not document.get("success"):
        raise ValueError("Cloudflare rejected the token while listing zones; ensure it has Zone Read for the target zone.")
    zones = sorted(
        {
            (
                item.get("name", "").strip().lower(),
                (item.get("account") or {}).get("id", "").strip(),
                (item.get("account") or {}).get("name", "").strip(),
            )
            for item in document.get("result", [])
            if item.get("name") and (item.get("account") or {}).get("id")
        }
    )
    if not zones:
        raise ValueError("Cloudflare returned no active zones for this token.")
    return zones


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--token-stdin", action="store_true", help="Read the API token from standard input")
    parser.add_argument("--fixture", action="store_true", help="Return deterministic non-provider dry-run zones")
    parser.add_argument("--details", action="store_true", help="Include the owning account ID and name as TSV")
    args = parser.parse_args()
    if args.fixture:
        fixtures = (
            ("example.net", "dry-run-account-a", "Adaetum test account"),
            ("homelab.example.org", "dry-run-account-b", "Homelab account"),
        )
        for zone, account_id, account_name in fixtures:
            print(f"{zone}\t{account_id}\t{account_name}" if args.details else zone)
        return 0
    if not args.token_stdin:
        parser.error("--token-stdin is required")
    token = sys.stdin.read().strip()
    if not token:
        raise SystemExit("Cloudflare token is empty")
    for zone, account_id, account_name in zones_from_api(token):
        print(f"{zone}\t{account_id}\t{account_name}" if args.details else zone)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValueError as exc:
        raise SystemExit(str(exc))
