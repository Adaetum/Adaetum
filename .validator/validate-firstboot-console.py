#!/usr/bin/env python3
"""Protect the first-boot console's runner-to-renderer status contract."""
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RUNNER = ROOT / "ansible" / "ansible-scripts" / "bundle-bootstrap"
CONSOLE = ROOT / "ks-src" / "fragments" / "shared" / "portable" / "11-tailscale-firstboot-lib.shfrag"


def main() -> int:
    runner = RUNNER.read_text(encoding="utf-8")
    console = CONSOLE.read_text(encoding="utf-8")
    fields = (
        "BOOTSTRAP_PROGRESS_PHASE_DETAIL",
        "BOOTSTRAP_PROGRESS_PHASE_LOG_PATH",
        "BOOTSTRAP_PROGRESS_PHASE_STARTED_AT",
        "BOOTSTRAP_PROGRESS_UPDATED_AT",
        "BOOTSTRAP_PROGRESS_RUN_STARTED_AT",
    )
    for field in fields:
        if f"printf '{field}=%q\\n'" not in runner:
            raise SystemExit(f"first-boot console contract failed: runner does not publish {field}")
        if field not in console:
            raise SystemExit(f"first-boot console contract failed: console does not consume {field}")
    for required in (
        "ui_latest_activity()",
        "ui_log_freshness()",
        "Live console follows",
        "ui_progress_bar",
    ):
        if required not in console:
            raise SystemExit(f"first-boot console contract failed: missing {required}")
    print("first-boot console contract passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
