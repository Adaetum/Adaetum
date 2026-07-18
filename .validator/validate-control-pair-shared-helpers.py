#!/usr/bin/env python3
"""Protect shared bootstrap helper ownership and security-sensitive behavior.

The control-pair library is the single owner for helpers whose implementation is
identical in both phases. Phase-specific helpers may intentionally differ in
mode handling, logging, and handoff policy, so this validator only rejects
byte-for-byte duplicates.
"""

from __future__ import annotations

import re
import shlex
import subprocess
import sys
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
PHASE_50 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-50/run-phase50.sh"
PHASE_60 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-60/run-phase60.sh"
SHARED_HELPERS = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/control-pair-common.sh"
FUNCTION_START = re.compile(
    r"^(?:(?:function)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\(\)\s*\{", re.MULTILINE
)


def functions(path: Path) -> dict[str, str]:
    """Return shell-function source keyed by name.

    Adaetum's bootstrap functions use balanced braces. This small parser also
    ignores braces inside quoted strings, which is enough for this structural
    repository check without executing any shell code.
    """
    source = path.read_text(encoding="utf-8")
    result: dict[str, str] = {}
    for match in FUNCTION_START.finditer(source):
        depth = 0
        quote: str | None = None
        index = match.end() - 1
        while index < len(source):
            character = source[index]
            line_start = source.rfind("\n", 0, index) + 1
            if (
                quote is None
                and character == "#"
                and source[line_start:index].strip() == ""
            ):
                next_line = source.find("\n", index)
                index = len(source) if next_line == -1 else next_line
                continue
            if quote:
                if character == "\\":
                    index += 1
                elif character == quote:
                    quote = None
            elif character in "'\"":
                quote = character
            elif character == "{":
                depth += 1
            elif character == "}":
                depth -= 1
                if depth == 0:
                    result[match.group(1)] = source[match.start() : index + 1]
                    break
            index += 1
        else:
            raise ValueError(f"{path}: could not find the end of {match.group(1)}")
    return result


def validate_gitea_token_helpers() -> list[str]:
    """Exercise scope rejection and post-promotion stale-token revocation."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

MOCK_MODE=readonly
curl() {{
  if [[ " $* " == *" --request DELETE "* ]]; then
    MOCK_MODE=revoke-after
    return 0
  fi
  case "${{MOCK_MODE}}" in
    readonly|revoke-after)
      printf '%s' '[{{"id":2,"name":"homepage-widget-2","token_last_eight":"abcdefgh","scopes":["read:issue","read:repository","read:notification"]}}]'
      ;;
    broad)
      printf '%s' '[{{"id":1,"name":"homepage-widget","token_last_eight":"abcdefgh","scopes":["all"]}}]'
      ;;
    revoke-before)
      printf '%s' '[{{"id":1,"name":"homepage-widget","token_last_eight":"oldtoken","scopes":["all"]}},{{"id":2,"name":"homepage-widget-2","token_last_eight":"abcdefgh","scopes":["read:issue","read:repository","read:notification"]}}]'
      ;;
  esac
}}

gitea_widget_token_has_required_scopes \
  http://gitea gitea-admin password 0123456789abcdefgh
MOCK_MODE=broad
if gitea_widget_token_has_required_scopes \
  http://gitea gitea-admin password 0123456789abcdefgh; then
  echo 'all-scope token was accepted' >&2
  exit 1
fi
MOCK_MODE=revoke-before
revoke_stale_gitea_widget_tokens \
  http://gitea gitea-admin password 0123456789abcdefgh
[[ "${{MOCK_MODE}}" == revoke-after ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"Gitea token helper behavior failed: {detail}"]


def validate_push_mirror_helper() -> list[str]:
    """Prove OpenBao wins on rerun and the hook reads only its projection."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

openbao_token=bao-token
kubectl_bin=mock_kubectl
OPENBAO_NAMESPACE=openbao
OPENBAO_POD=openbao-0
EXEC_VALIDATED=0

read_openbao_app_field() {{
  case "$2" in
    remote_url) printf '%s' 'https://github.com/example/recovery.git' ;;
    username) printf '%s' 'openbao-user' ;;
    token) printf '%s' 'openbao-token-value' ;;
  esac
}}
seed_openbao_app_fields() {{
  echo 'existing OpenBao push-mirror fields were unexpectedly reseeded' >&2
  return 1
}}
repo_url_is_github() {{ return 0; }}
github_token_looks_like_pat() {{ return 0; }}
find_ready_gitea_pod() {{ printf '%s' gitea-0; }}

mock_kubectl() {{
  if [[ " $* " == *" exec -i gitea-0 "* ]]; then
    [[ " $* " == *" EXPECTED_REPO_URL=https://github.com/example/recovery.git "* ]]
    [[ " $* " == *" EXPECTED_USERNAME=openbao-user "* ]]
    [[ " $* " != *"openbao-token-value"* ]]
    payload="$(cat)"
    grep -Fq 'credential_dir="/var/run/adaetum/push-mirror"' <<<"${{payload}}"
    grep -Fq 'cat "${{credential_dir}}/token"' <<<"${{payload}}"
    grep -Fq 'rm -f' <<<"${{payload}}"
    grep -Fq '.github-mirror.password' <<<"${{payload}}"
    EXEC_VALIDATED=1
    return 0
  fi
  if [[ " $* " == *" get secret gitea-push-mirror "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get externalsecret gitea-push-mirror "* ]]; then
    if [[ " $* " == *" jsonpath="* ]]; then
      printf 'True\tReady\tOpenBao delivery ready'
    fi
    return 0
  fi
  if [[ " $* " == *" apply -f - "* ]]; then
    cat >/dev/null
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

configure_gitea_push_mirror_from_openbao \
  phase-test gitea-admin cluster \
  https://github.com/stale/recovery.git stale-user stale-token
[[ "${{EXEC_VALIDATED}}" == 1 ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"Gitea push-mirror helper behavior failed: {detail}"]


def main() -> int:
    phase_50_functions = functions(PHASE_50)
    phase_60_functions = functions(PHASE_60)
    duplicates = sorted(
        name
        for name, implementation in phase_50_functions.items()
        if phase_60_functions.get(name) == implementation
    )
    failures = validate_gitea_token_helpers()
    failures.extend(validate_push_mirror_helper())
    if duplicates:
        print("Move exact shared helpers into control-pair-common.sh:", file=sys.stderr)
        print("\n".join(f"- {name}" for name in duplicates), file=sys.stderr)
        failures.append("Phase 50/60 contain byte-identical helper implementations")
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print(
        "Control-pair helper ownership ok: "
        f"{len(phase_50_functions)} Phase 50 and {len(phase_60_functions)} Phase 60 helpers; "
        "Gitea token and push-mirror behavior passed."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
