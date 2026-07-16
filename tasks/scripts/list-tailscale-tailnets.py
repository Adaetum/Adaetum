#!/usr/bin/env python3
"""Discover MagicDNS tailnet domains without displaying an access token."""
from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request


API_URL = "https://api.tailscale.com/api/v2/tailnet/-/devices"


def tailnets_from_api(token: str) -> list[str]:
    request = urllib.request.Request(API_URL)
    request.add_header("Authorization", f"Bearer {token}")
    request.add_header("User-Agent", "adaetum-first-run")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            document = json.load(response)
    except (OSError, urllib.error.HTTPError, json.JSONDecodeError) as exc:
        raise ValueError(f"unable to read Tailscale devices: {exc}") from exc

    tailnets = set()
    for device in document.get("devices", []):
        dns_name = str(device.get("dnsName", "")).strip().lower().rstrip(".")
        if "." in dns_name:
            tailnets.add(dns_name.split(".", 1)[1])
    if not tailnets:
        raise ValueError(
            "Tailscale returned no MagicDNS names. Add or authorize one device in this tailnet, then rerun task init."
        )
    return sorted(tailnets)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--token-stdin", action="store_true", help="Read the access token from standard input")
    parser.add_argument("--fixture", action="store_true", help="Return deterministic non-provider dry-run tailnets")
    args = parser.parse_args()
    if args.fixture:
        print("tailnet-a1b2.ts.net")
        print("tailnet-c3d4.ts.net")
        return 0
    if not args.token_stdin:
        parser.error("--token-stdin is required")
    token = sys.stdin.read().strip()
    if not token:
        raise SystemExit("Tailscale token is empty")
    for tailnet in tailnets_from_api(token):
        print(tailnet)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ValueError as exc:
        raise SystemExit(str(exc))
