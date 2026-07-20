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
PHASE_40 = REPOSITORY_ROOT / "ansible/ansible-scripts/bootstrap/Phase-40/run-phase40.sh"
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
repo_root={shlex.quote(str(REPOSITORY_ROOT))}
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
  if [[ " $* " == *" get crd externalsecrets.external-secrets.io "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get deployment external-secrets "* && " $* " == *".status.conditions"* ]]; then
    printf '%s' True
    return 0
  fi
  if [[ " $* " == *" apply -f "*"push-mirror-external-secret.yaml"* ]]; then
    return 0
  fi
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


def validate_external_secret_timeout() -> list[str]:
    """Prove a controller that never reports status cannot block boot forever."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

BOOTSTRAP_EXTERNAL_SECRET_TIMEOUT_SECONDS=1
BOOTSTRAP_EXTERNAL_SECRET_POLL_SECONDS=1
DIAGNOSTICS_CAPTURED=0

bootstrap_capture_external_secret_diagnostics() {{
  [[ "$5" == "secret-sync-timeout" ]]
  DIAGNOSTICS_CAPTURED=1
}}

mock_kubectl() {{
  if [[ " $* " == *" get externalsecret stalled "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get secret stalled-target "* ]]; then
    return 1
  fi
  if [[ " $* " == *" get deployment external-secrets "* ]]; then
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

if bootstrap_wait_for_external_secret_delivery \
  mock_kubectl ansible stalled stalled-target stalled-component; then
  echo 'ExternalSecret without controller status unexpectedly passed' >&2
  exit 1
fi
[[ "${{DIAGNOSTICS_CAPTURED}}" == 1 ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=5,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"ExternalSecret timeout behavior failed: {detail}"]


def validate_csi_secret_timeout() -> list[str]:
    """Prove a missing CSI class cannot block the first boot indefinitely."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

BOOTSTRAP_CSI_SECRET_TIMEOUT_SECONDS=1
BOOTSTRAP_CSI_SECRET_POLL_SECONDS=1
DIAGNOSTICS_CAPTURED=0

bootstrap_capture_secret_delivery_foundation_diagnostics() {{
  [[ "$3" == "provider-class-missing" ]]
  DIAGNOSTICS_CAPTURED=1
}}

mock_kubectl() {{
  if [[ " $* " == *" get secretproviderclass missing-class "* ]]; then
    return 1
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

if bootstrap_wait_for_csi_secret_delivery \
  mock_kubectl ansible missing-class stalled-component; then
  echo 'missing SecretProviderClass unexpectedly passed' >&2
  exit 1
fi
[[ "${{DIAGNOSTICS_CAPTURED}}" == 1 ]]
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=5,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"CSI secret timeout behavior failed: {detail}"]


def validate_secret_foundation_ready() -> list[str]:
    """Exercise the successful status contract used by Phases 60 and 70."""
    source_path = shlex.quote(str(SHARED_HELPERS))
    script = rf'''
set -euo pipefail
source {source_path}

mock_kubectl() {{
  if [[ " $* " == *" -n argocd get application "* && " $* " == *".status.sync.status"* ]]; then
    printf '%s' Synced
    return 0
  fi
  if [[ " $* " == *" -n argocd get application "* && " $* " == *".status.health.status"* ]]; then
    printf '%s' Healthy
    return 0
  fi
  if [[ " $* " == *" get crd "* ]]; then
    return 0
  fi
  if [[ " $* " == *" get clustersecretstore openbao "* ]]; then
    printf '%s' True
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}}

bootstrap_wait_for_secret_delivery_foundation mock_kubectl test-foundation
'''
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=REPOSITORY_ROOT,
        text=True,
        capture_output=True,
        check=False,
        timeout=5,
    )
    if result.returncode == 0:
        return []
    detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
    return [f"Secret-delivery foundation readiness behavior failed: {detail}"]


def validate_phase70_realization_gate() -> list[str]:
    """Keep the post-handoff proof strict, bounded, and non-restarting."""
    phase_60 = PHASE_60.read_text(encoding="utf-8")
    phase_70 = (
        REPOSITORY_ROOT
        / "ansible/ansible-scripts/bootstrap/Phase-70/run-phase70.sh"
    ).read_text(encoding="utf-8")
    failures: list[str] = []

    for required in (
        "require_gitea_service_contract()",
        'get service "${GITEA_INTERNAL_SERVICE_NAME}"',
        'get endpoints "${GITEA_INTERNAL_SERVICE_NAME}"',
        "Gitea service discovery does not resolve to a ready registry endpoint",
    ):
        if required not in phase_60:
            failures.append(
                f"Phase 60 Gitea service preflight contract missing: {required}"
            )

    for required in (
        "PHASE60_WARNING_ONLY=0",
        'PHASE60_GITEA_ROLLOUT_TIMEOUT="${PHASE70_GITEA_ROLLOUT_TIMEOUT:-120s}"',
        "PHASE60_GITEA_ROLLOUT_ATTEMPTS=1",
        "PHASE60_GITEA_ROLLOUT_RESTART_ON_FAIL=0",
        "GitOps handoff verification failed; stopping before realization checks",
        "verify_secret_delivery_foundation_phase70",
        "secret-delivery foundation failed; stopping before workload realization checks",
        "GitOps realization checks failed; stopping before secret-delivery reconciliation",
    ):
        if required not in phase_70:
            failures.append(f"Phase 70 realization gate contract missing: {required}")

    foundation_gate = phase_70.find(
        'run_phase70_step "verify secret-delivery foundation"'
    )
    realization_gate = phase_70.find(
        'run_phase70_step "run GitOps realization checks"'
    )
    if foundation_gate < 0 or realization_gate < 0 or foundation_gate > realization_gate:
        failures.append(
            "Phase 70 must prove the secret-delivery foundation before workload realization"
        )

    phase_60_functions = functions(PHASE_60)
    service_check = phase_60_functions.get("require_gitea_service_contract")
    if service_check:
        script = service_check + r'''
set -euo pipefail
kubectl_bin=mock_kubectl
GITEA_INTERNAL_SERVICE_NAME=gitea-http
MOCK_STATE=healthy
DEBUG_REASON=""

gitea_debug_dump() {
  DEBUG_REASON="$1"
}

mock_kubectl() {
  if [[ " $* " == *" get service gitea-http "* ]]; then
    if [[ "${MOCK_STATE}" == "missing" ]]; then
      return 1
    fi
    if [[ "$*" == *".spec.clusterIP"* ]]; then
      # The pinned Gitea chart uses a valid headless Service.
      printf '%s' 'None'
    elif [[ "$*" == *".spec.ports"* ]]; then
      printf '%s' '3000 '
    fi
    return 0
  fi
  if [[ " $* " == *" get endpoints gitea-http "* ]]; then
    if [[ "${MOCK_STATE}" == "no-endpoint" ]]; then
      return 0
    fi
    printf '%s' '10.42.148.10 '
    return 0
  fi
  echo "unexpected mock kubectl call: $*" >&2
  return 1
}

require_gitea_service_contract

MOCK_STATE=missing
if require_gitea_service_contract; then
  echo 'missing Gitea Service unexpectedly passed' >&2
  exit 1
fi
[[ "${DEBUG_REASON}" == "service-missing" ]]

MOCK_STATE=no-endpoint
DEBUG_REASON=""
if require_gitea_service_contract; then
  echo 'Gitea Service without a ready endpoint unexpectedly passed' >&2
  exit 1
fi
[[ "${DEBUG_REASON}" == "service-has-no-ready-endpoint" ]]
'''
        result = subprocess.run(
            ["bash", "-c", script],
            cwd=REPOSITORY_ROOT,
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "unknown failure"
            failures.append(f"Gitea service preflight behavior failed: {detail}")

    return failures


def validate_secret_foundation_handoff() -> list[str]:
    """Keep the narrowed source-of-truth ApplicationSet ahead of full handoff."""
    phase_60 = PHASE_60.read_text(encoding="utf-8")
    phase_50 = PHASE_50.read_text(encoding="utf-8")
    phase_40 = PHASE_40.read_text(encoding="utf-8")
    failures: list[str] = []
    for required in (
        "applicationset-foundation.yaml",
        "pods/secrets/csi-secrets-store.app.yaml",
        "pods/secrets/openbao-csi-provider.app.yaml",
        "pods/secrets/external-secrets.app.yaml",
        "pods/secrets/openbao-sync.app.yaml",
        "bootstrap_wait_for_secret_delivery_foundation",
        "get applicationset apps",
        "preserving its generator while checking foundation readiness",
    ):
        if required not in phase_60:
            failures.append(f"Phase 60 secret-foundation handoff missing: {required}")
    foundation_apply = phase_60.find(
        'apply_secret_delivery_foundation_bridge "${rendered_dir}/applicationset-foundation.yaml"'
    )
    full_apply = phase_60.find('apply -f "${rendered_dir}/app-of-apps.yaml"')
    if foundation_apply < 0 or full_apply < 0 or foundation_apply > full_apply:
        failures.append(
            "Phase 60 must prove its narrowed ApplicationSet before applying app-of-apps"
        )
    if 'wait_for_argo_application_crd "180s"' in phase_40:
        failures.append(
            "Phase 40 must not wait for the Argo CRD before the phase that installs Argo CD"
        )
    for phase_name, source in (("Phase 50", phase_50), ("Phase 60", phase_60)):
        for required in (
            "get crd externalsecrets.external-secrets.io",
            "deferring admin token reconciliation to Phase 90",
        ):
            if required not in source:
                failures.append(f"{phase_name} clean-install deferral missing: {required}")
    return failures


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
    failures.extend(validate_external_secret_timeout())
    failures.extend(validate_csi_secret_timeout())
    failures.extend(validate_secret_foundation_ready())
    failures.extend(validate_phase70_realization_gate())
    failures.extend(validate_secret_foundation_handoff())
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
        "Gitea token, push-mirror, bounded secret delivery, and Phase 70 realization behavior passed."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
