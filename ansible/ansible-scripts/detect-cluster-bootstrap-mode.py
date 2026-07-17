#!/usr/bin/env python3
"""Choose first-primary or join mode from Tailscale peer tags.

Reachability is deliberately irrelevant. Any other device carrying both the
cluster tag and ``tag:server`` proves that a cluster primary has already been
declared, even when that device is offline. Treating an offline server as
absent could create a second independent cluster during an outage.
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any


def normalize_tag(tag: str) -> str:
    value = tag.strip()
    if value and not value.startswith("tag:"):
        value = f"tag:{value}"
    return value


def detect_mode(status: dict[str, Any], cluster_tag: str) -> str:
    """Return ``join`` when any peer is a server in this cluster, else ``start``."""
    required_cluster_tag = normalize_tag(cluster_tag)
    if not required_cluster_tag:
        raise ValueError("a Tailscale cluster tag is required")

    peers = status.get("Peer") or {}
    if not isinstance(peers, dict):
        raise ValueError("Tailscale status Peer data must be an object")

    for peer in peers.values():
        if not isinstance(peer, dict):
            continue
        tags = peer.get("Tags") or []
        if required_cluster_tag in tags and "tag:server" in tags:
            return "join"
    return "start"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cluster-tag", required=True)
    args = parser.parse_args()
    try:
        status = json.load(sys.stdin)
        if not isinstance(status, dict):
            raise ValueError("Tailscale status must be a JSON object")
        print(detect_mode(status, args.cluster_tag))
    except (json.JSONDecodeError, ValueError) as exc:
        print(f"bootstrap mode detection failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
