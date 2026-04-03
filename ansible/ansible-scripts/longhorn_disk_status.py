#!/usr/bin/env python3
"""Report Longhorn disk schedulability from a node JSON payload."""
from __future__ import annotations

import json
import os
import sys


def main() -> int:
    raw = os.environ.get("LH_NODE_JSON", "")
    if not raw:
        print("unknown")
        return 0
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        print("unknown")
        return 0

    disk_status = data.get("status", {}).get("diskStatus", {})
    if not disk_status:
        print("unknown")
        return 0

    for name, disk in disk_status.items():
        for condition in disk.get("conditions", []):
            if condition.get("type") == "Schedulable" and condition.get("status") == "False":
                print(f"unschedulable:{name}")
                return 0

    print("schedulable")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
