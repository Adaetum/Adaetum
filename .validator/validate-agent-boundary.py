#!/usr/bin/env python3
"""Enforce the agent's security boundary: who may run commands on a server.

The rule (adaetum/agent/docs/security-boundary.md): the agent does not run commands on
servers. Servers are changed by Ansible playbooks the agent launches. The only
exception is an explicit, off-by-default override in adaetum/agent/internal/hostaccess.

This check keeps that true as the code grows. It fails if any Go package other
than the ones listed below imports os/exec or an SSH library, or otherwise starts a
process, so a new way to run commands cannot appear without a reviewed change to
THIS file. It also checks that the override is wired only into the cluster agent.
"""

from __future__ import annotations

import argparse
import re
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
AGENT = Path("adaetum/agent")

# Packages that may start processes, and why. Changing this list is the review.
ALLOWED = {
    "adaetum/agent/internal/inspect": "read-only inspection: a fixed allowlist of commands, locally or over SSH in development",
    "adaetum/agent/internal/cleanup": "removes unused container images on the machine the agent itself runs on (root, flag, token)",
    "adaetum/agent/internal/ansiblerun": "launches ansible-playbook: the sanctioned way to change servers",
    "adaetum/agent/internal/hostaccess": "the optional, off-by-default SSH override",
    "adaetum/agent/cmd/adaetum-agent": "only exec.LookPath, to check that a program exists",
}

FORBIDDEN_IMPORTS = ("os/exec", "golang.org/x/crypto/ssh")
FORBIDDEN_CALLS = re.compile(r"\b(syscall\.(Exec|ForkExec|StartProcess)|os\.StartProcess)\b")
IMPORT_LINE = re.compile(r'^\s*(?:[A-Za-z_.]+\s+)?"([^"]+)"\s*$')
# In the command package, the only allowed use of os/exec is LookPath.
CMD_EXEC_USE = re.compile(r"\bexec\.(?!LookPath\b)\w+")

HOSTACCESS_IMPORT = "internal/hostaccess"
# Files allowed to wire the override. The host agent (serve) must not.
HOSTACCESS_WIRING = {"adaetum/agent/cmd/adaetum-agent/hostaccess.go", "adaetum/agent/cmd/adaetum-agent/cluster.go"}


def go_files(root: Path):
    for path in sorted((root / AGENT).rglob("*.go")):
        if path.name.endswith("_test.go") or "node_modules" in path.parts:
            continue
        yield path


def imports_of(source: str) -> list[str]:
    found: list[str] = []
    in_block = False
    for line in source.splitlines():
        stripped = line.strip()
        if stripped.startswith("import ("):
            in_block = True
            continue
        if in_block:
            if stripped == ")":
                in_block = False
                continue
            match = IMPORT_LINE.match(line)
            if match:
                found.append(match.group(1))
        elif stripped.startswith("import "):
            match = re.search(r'"([^"]+)"', stripped)
            if match:
                found.append(match.group(1))
    return found


def check(root: Path) -> list[str]:
    problems: list[str] = []
    for path in go_files(root):
        relative = path.relative_to(root).as_posix()
        package = str(Path(relative).parent.as_posix())
        source = path.read_text(encoding="utf-8")
        imports = imports_of(source)

        for forbidden in FORBIDDEN_IMPORTS:
            if forbidden in imports and package not in ALLOWED:
                problems.append(
                    f"{relative}: imports {forbidden}, but {package} is not allowed to run commands. "
                    "Change servers by launching an Ansible playbook (adaetum/agent/internal/ansiblerun) instead. "
                    "If a new package truly must run commands, add it to ALLOWED in "
                    ".validator/validate-agent-boundary.py with a reason, as a reviewed change."
                )
        if FORBIDDEN_CALLS.search(source) and package not in ALLOWED:
            problems.append(f"{relative}: starts a process, which {package} may not do (see adaetum/agent/docs/security-boundary.md)")

        if package == "adaetum/agent/cmd/adaetum-agent" and "os/exec" in imports:
            for match in CMD_EXEC_USE.finditer(source):
                problems.append(f"{relative}: uses {match.group(0)}; the command package may only use exec.LookPath")

        if any(i.endswith(HOSTACCESS_IMPORT) for i in imports) and relative not in HOSTACCESS_WIRING and package != "adaetum/agent/internal/hostaccess":
            problems.append(
                f"{relative}: imports the SSH override. Only the cluster agent's wiring "
                f"({', '.join(sorted(HOSTACCESS_WIRING))}) may; the host agent must not offer it."
            )

    main = root / AGENT / "cmd/adaetum-agent/main.go"
    if main.exists() and "setupHostAccess" in main.read_text(encoding="utf-8"):
        problems.append("adaetum/agent/cmd/adaetum-agent/main.go wires the SSH override into the host agent (serve); it belongs to the cluster agent only")

    if not (root / AGENT / "docs/security-boundary.md").exists():
        problems.append("adaetum/agent/docs/security-boundary.md is missing: the boundary must stay documented")
    return problems


def self_test() -> list[str]:
    """Prove the check can fail: a violating tree must be caught, a clean one must pass."""
    failures: list[str] = []

    def tree(files: dict[str, str]) -> Path:
        base = Path(tempfile.mkdtemp())
        (base / "adaetum/agent/docs").mkdir(parents=True)
        (base / "adaetum/agent/docs/security-boundary.md").write_text("x")
        for name, content in files.items():
            target = base / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content)
        return base

    sneaky = 'package cluster\n\nimport (\n\t"fmt"\n\t"os/exec"\n)\n\nfunc f() { exec.Command("id") }\n'
    if not check(tree({"adaetum/agent/internal/cluster/x.go": sneaky})):
        failures.append("a cluster package importing os/exec was accepted")
    if not check(tree({"adaetum/agent/internal/fleet/x.go": 'package fleet\nimport "golang.org/x/crypto/ssh"\n'})):
        failures.append("an SSH library import outside the allowed packages was accepted")
    if not check(tree({"adaetum/agent/internal/store/x.go": "package store\nfunc f() { syscall.Exec(\"/bin/sh\", nil, nil) }\n"})):
        failures.append("syscall.Exec outside the allowed packages was accepted")
    if not check(tree({"adaetum/agent/cmd/adaetum-agent/x.go": 'package main\nimport "os/exec"\nfunc f() { exec.Command("id") }\n'})):
        failures.append("exec.Command in the command package was accepted")
    if not check(tree({"adaetum/agent/cmd/adaetum-agent/main.go": "package main\nfunc main() { setupHostAccess() }\n"})):
        failures.append("the SSH override wired into the host agent was accepted")
    if not check(tree({"adaetum/agent/internal/webui/x.go": 'package webui\nimport "github.com/Adaetum/Adaetum/agent/internal/hostaccess"\n'})):
        failures.append("the SSH override imported by an unrelated package was accepted")
    clean = tree({
        "adaetum/agent/internal/ansiblerun/x.go": 'package ansiblerun\nimport "os/exec"\nfunc f() { exec.Command("ansible-playbook") }\n',
        "adaetum/agent/cmd/adaetum-agent/x.go": 'package main\nimport "os/exec"\nfunc f() { exec.LookPath("ssh") }\n',
        "adaetum/agent/cmd/adaetum-agent/hostaccess.go": 'package main\nimport "github.com/Adaetum/Adaetum/agent/internal/hostaccess"\n',
    })
    if check(clean):
        failures.append(f"a compliant tree was rejected: {check(clean)}")
    return failures


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--self-test", action="store_true", help="prove the check rejects violations")
    args = parser.parse_args()

    if args.self_test:
        failures = self_test()
        if failures:
            print("agent boundary self-test failed:", file=sys.stderr)
            print("\n".join(f"- {f}" for f in failures), file=sys.stderr)
            return 1
    problems = check(ROOT)
    if problems:
        print("agent security boundary violated:", file=sys.stderr)
        print("\n".join(f"- {p}" for p in problems), file=sys.stderr)
        return 1
    print("agent security boundary holds: only the listed packages can run commands")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
