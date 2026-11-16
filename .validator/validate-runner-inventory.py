#!/usr/bin/env python3
"""The ansible runner must never manage a container as if it were its node.

build-inventory marks the machine it runs on as the local node. On the node itself (break-glass)
"ansible_connection: local" is right. Inside the runner pod it is wrong: the pod shares the node's
network but not its filesystem, so "local" would run every playbook against the container. In the
pod (ANSIBLE_LOCAL_SSH_KEY set) the node must be reached over SSH to loopback with that key, and a
node whose key is missing must be left out rather than run as local.
"""
from __future__ import annotations

import os
import socket
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = REPO_ROOT / "ansible" / "ansible-scripts" / "build-inventory"


def build(env_extra: dict[str, str]) -> tuple[str, str]:
    with tempfile.TemporaryDirectory() as root:
        env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": root, **env_extra}
        result = subprocess.run(["sh", str(SCRIPT), root], env=env, capture_output=True, text=True, check=False)
        if result.returncode != 0:
            raise SystemExit(f"build-inventory failed ({result.returncode}): {result.stderr.strip()}")
        return (Path(root) / "node-inventory.yml").read_text(encoding="utf-8"), result.stderr


def main() -> int:
    failures: list[str] = []
    host = socket.gethostname().split(".")[0]

    on_node, _ = build({})
    if "ansible_connection: 'local'" not in on_node or "ansible_connection: 'ssh'" in on_node:
        failures.append("on the node itself the local host must use ansible_connection: local")

    with tempfile.NamedTemporaryFile() as key:
        in_pod, _ = build({"ANSIBLE_LOCAL_SSH_KEY": key.name})
        if "ansible_connection: 'local'" in in_pod:
            failures.append("in the runner pod the node must never be managed as local (that is the container)")
        for needed in ("ansible_connection: 'ssh'", "ansible_host: '127.0.0.1'", f"ansible_ssh_private_key_file: '{key.name}'", host):
            if needed not in in_pod:
                failures.append(f"in the runner pod the inventory is missing: {needed}")

    missing = "/nonexistent/runner-key"
    no_key, warning = build({"ANSIBLE_LOCAL_SSH_KEY": missing})
    if host in no_key or "ansible_connection: 'local'" in no_key:
        failures.append("a node whose runner key is missing must be left out, not run as local")
    if "not managing" not in warning:
        failures.append("a node left out for a missing key must say so")

    # Cron gives its jobs an almost empty environment; the key variable only reaches build-inventory
    # if the entrypoint writes it into the cron file. Without it the pod falls back to "local".
    entrypoint = (REPO_ROOT / "ansible" / "ansible-scripts" / "cron-entrypoint").read_text(encoding="utf-8")
    if "ANSIBLE_LOCAL_SSH_KEY=${local_ssh_key}" not in entrypoint:
        failures.append("cron-entrypoint must pass ANSIBLE_LOCAL_SSH_KEY to the scheduled job")
    for manifest in ("ansible-runner-deployment.yaml", "ansible-runner-deployment.yaml.tmpl"):
        if "ANSIBLE_LOCAL_SSH_KEY" not in (REPO_ROOT / "pods" / "ansible" / "ansible" / manifest).read_text(encoding="utf-8"):
            failures.append(f"{manifest} must set ANSIBLE_LOCAL_SSH_KEY so the pod manages its node over SSH")

    for failure in failures:
        print(f"validate-runner-inventory: {failure}", file=sys.stderr)
    if failures:
        return 1
    print("runner inventory holds: local on the node, SSH to loopback in the pod, nothing run as local without a key")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
