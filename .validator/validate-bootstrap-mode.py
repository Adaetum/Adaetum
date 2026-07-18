#!/usr/bin/env python3
"""Regression checks for the Tailscale-backed first-primary decision."""

from __future__ import annotations

import importlib.util
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DETECTOR = ROOT / "ansible" / "ansible-scripts" / "detect-cluster-bootstrap-mode.py"


def load_detector():
    spec = importlib.util.spec_from_file_location("detect_cluster_bootstrap_mode", DETECTOR)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {DETECTOR}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def peer(tags: list[str], online: bool) -> dict[str, object]:
    return {"Tags": tags, "Online": online, "HostName": "existing-server"}


def main() -> int:
    detector = load_detector()
    cases = (
        ("no peers starts the first cluster", {"Peer": {}}, "start"),
        (
            "an online tagged server joins",
            {"Peer": {"server": peer(["tag:cluster", "tag:server"], True)}},
            "join",
        ),
        (
            "an offline tagged server still joins",
            {"Peer": {"server": peer(["tag:cluster", "tag:server"], False)}},
            "join",
        ),
        (
            "the cluster tag alone does not declare a primary",
            {"Peer": {"agent": peer(["tag:cluster", "tag:agent"], True)}},
            "start",
        ),
        (
            "a server in another cluster does not declare this primary",
            {"Peer": {"server": peer(["tag:other", "tag:server"], True)}},
            "start",
        ),
    )
    for label, status, expected in cases:
        actual = detector.detect_mode(status, "tag:cluster")
        if actual != expected:
            raise AssertionError(f"{label}: expected {expected}, got {actual}")
    print("bootstrap mode contract passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
