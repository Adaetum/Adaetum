#!/usr/bin/env python3

import os
import subprocess
import sys
from pathlib import Path


def main() -> int:
    repo_root = Path(__file__).resolve().parents[2]
    ansible_dir = repo_root / "ansible"

    playbooks = [
        ansible_dir / "playbooks" / "bootstrap.yml",
        ansible_dir / "playbooks" / "healthcheck.yml",
        ansible_dir / "playbooks" / "argocd.yml",
        ansible_dir / "playbooks" / "platform-bootstrap.yml",
    ]

    env = os.environ.copy()
    env["ANSIBLE_CONFIG"] = str(ansible_dir / "ansible.cfg")
    env["PYTHONUTF8"] = "1"
    env["PYTHONIOENCODING"] = "utf-8"
    env["ANSIBLE_ROLES_PATH"] = ":".join(
        [
            str(ansible_dir / "automation-roles"),
            str(ansible_dir / "playbooks" / "roles"),
            "/etc/ansible/roles",
            "/usr/share/ansible/roles",
        ]
    )

    inventory = env.get("ANSIBLE_INVENTORY", "localhost,")

    for playbook in playbooks:
        if not playbook.is_file():
            continue
        cmd = [
            sys.executable,
            "-X",
            "utf8",
            "-m",
            "ansible.cli.playbook",
            "-i",
            inventory,
            "--syntax-check",
            str(playbook),
        ]
        proc = subprocess.run(
            cmd,
            cwd=str(ansible_dir),
            env=env,
            capture_output=True,
            text=True,
        )
        if proc.returncode != 0:
            output = (proc.stdout or "") + (proc.stderr or "")
            if (
                os.name == "nt"
                and (
                    "requires the locale encoding to be UTF-8" in output
                    or "os.get_blocking" in output
                    or "WinError 87" in output
                )
            ):
                print(
                    "[pre-commit] Skipping ansible syntax-check in unsupported Windows shell environment.",
                    file=sys.stderr,
                )
                print(output.strip(), file=sys.stderr)
                return 0
            if proc.stdout:
                print(proc.stdout, end="")
            if proc.stderr:
                print(proc.stderr, end="", file=sys.stderr)
            return proc.returncode

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
