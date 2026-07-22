#!/usr/bin/env bash
set -euo pipefail

# Validate a runtime payload without installing a machine. When no payload is
# present, generate a temporary one and remove it on exit so validation remains
# a read-only operation from the operator's perspective.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

# Installed hosts must validate the exact private payload they downloaded.
# Local setup and CI keep the generated dist payload as their default.
out_file="${1:-${BOOTSTRAP_RUNTIME_VALIDATE_FILE:-dist/bootstrap-runtime.env}}"
require_existing_payload=0
if [ "${#}" -gt 0 ] || [ -n "${BOOTSTRAP_RUNTIME_VALIDATE_FILE:-}" ]; then
  require_existing_payload=1
fi
tmp_generated=0
kickstart_path="dist/ks-templates/rocky10.ks"

cleanup() {
  # Never delete an operator's existing payload; only remove the one this check
  # created as a convenience.
  if [ "${tmp_generated}" = "1" ] && [ -f "${out_file}" ]; then
    rm -f "${out_file}"
  fi
}
trap cleanup EXIT

normalize_value() {
  local value="${1:-}"
  printf '%s' "${value}" | tr -d '\r\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//; s/^"(.*)"$/\1/; s/^\x27(.*)\x27$/\1/'
}

read_env_value() {
  local file="$1"
  local key="$2"
  local value=""
  if [ -f "${file}" ]; then
    value="$(awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${file}")"
  fi
  normalize_value "${value}"
}

github_token_looks_git_capable() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*|ghs_*) return 0 ;;
    *) return 1 ;;
  esac
}

if [ ! -f "${out_file}" ] && [ "${require_existing_payload}" = "1" ]; then
  echo "Missing runtime payload: ${out_file}" >&2
  exit 1
fi

if [ ! -f "${out_file}" ]; then
  bash ./tasks/scripts/build-bootstrap-runtime-env.sh "${out_file}" >/dev/null
  tmp_generated=1
fi

if [ ! -f "${out_file}" ]; then
  echo "Missing runtime payload: ${out_file}" >&2
  exit 1
fi

backup_plain="$(read_env_value "${out_file}" BOOTSTRAP_BACKUP_PASSPHRASE)"
backup_b64="$(read_env_value "${out_file}" BOOTSTRAP_BACKUP_PASSPHRASE_B64)"
backup_enabled="$(read_env_value "${out_file}" BOOTSTRAP_BACKUP_TO_R2)"
backup_url="$(read_env_value "${out_file}" BOOTSTRAP_BACKUP_URL)"
argocd_token="$(read_env_value "${out_file}" ARGOCD_GITHUB_TOKEN)"
seed_token="$(read_env_value "${out_file}" GITEA_SEED_SOURCE_TOKEN)"
mirror_enabled="$(read_env_value "${out_file}" GITEA_PUSH_MIRROR_ENABLED)"
mirror_repo_url="$(read_env_value "${out_file}" GITEA_PUSH_MIRROR_REPO_URL)"
mirror_username="$(read_env_value "${out_file}" GITEA_PUSH_MIRROR_USERNAME)"
mirror_token="$(read_env_value "${out_file}" GITEA_PUSH_MIRROR_TOKEN)"
bundle_url="$(read_env_value "${out_file}" BOOTSTRAP_BUNDLE_URL)"
config_contract="$(read_env_value "${out_file}" ADAETUM_CONFIG_CONTRACT)"

if [ "${config_contract}" = "platform/v1alpha1" ]; then
  python3 ./tasks/scripts/validate-platform-profile.py --profile ./platform.yaml
fi

if [ -z "${backup_plain}" ] && [ -z "${backup_b64}" ]; then
  echo "Runtime payload is missing BOOTSTRAP_BACKUP_PASSPHRASE and BOOTSTRAP_BACKUP_PASSPHRASE_B64." >&2
  exit 1
fi

case "$(printf '%s' "${backup_enabled}" | tr '[:upper:]' '[:lower:]')" in
  1|true|yes|on) ;;
  *)
    echo "Runtime payload must enable BOOTSTRAP_BACKUP_TO_R2 before Phase 99 can burn bootstrap authority." >&2
    exit 1
    ;;
esac
if ! printf '%s' "${backup_url}" | grep -Eq '^https?://[^[:space:]]+/backup$'; then
  echo "Runtime payload BOOTSTRAP_BACKUP_URL must be an HTTP(S) /backup endpoint without query credentials." >&2
  exit 1
fi

if ! github_token_looks_git_capable "${argocd_token}"; then
  echo "Runtime payload ARGOCD_GITHUB_TOKEN is missing or not a git-capable GitHub token." >&2
  exit 1
fi

if ! github_token_looks_git_capable "${seed_token}"; then
  echo "Runtime payload GITEA_SEED_SOURCE_TOKEN is missing or not a git-capable GitHub token." >&2
  exit 1
fi

if [ -f ".env" ]; then
  # The opinionated path derives all three seed credentials from one canonical
  # token. Enforce that relationship before secrets reach the installer.
  sync_token="$(read_env_value ".env" GITHUB_SYNC_TOKEN)"
  if github_token_looks_git_capable "${sync_token}"; then
    if [ "${argocd_token}" != "${sync_token}" ]; then
      echo "Runtime payload ARGOCD_GITHUB_TOKEN does not match canonical GITHUB_SYNC_TOKEN for the opinionated path." >&2
      exit 1
    fi
    if [ "${seed_token}" != "${sync_token}" ]; then
      echo "Runtime payload GITEA_SEED_SOURCE_TOKEN does not match canonical GITHUB_SYNC_TOKEN for the opinionated path." >&2
      exit 1
    fi
    case "$(printf '%s' "${mirror_enabled}" | tr '[:upper:]' '[:lower:]')" in
      1|true|yes) ;;
      *)
        echo "Runtime payload GITEA_PUSH_MIRROR_ENABLED must be enabled for the opinionated path." >&2
        exit 1
        ;;
    esac
    if [ -z "${mirror_repo_url}" ]; then
      echo "Runtime payload GITEA_PUSH_MIRROR_REPO_URL is blank for the opinionated path." >&2
      exit 1
    fi
    if [ -z "${mirror_username}" ]; then
      echo "Runtime payload GITEA_PUSH_MIRROR_USERNAME is blank for the opinionated path." >&2
      exit 1
    fi
    if [ "${mirror_token}" != "${sync_token}" ]; then
      echo "Runtime payload GITEA_PUSH_MIRROR_TOKEN does not match canonical GITHUB_SYNC_TOKEN for the opinionated path." >&2
      exit 1
    fi
  fi
fi

if printf '%s' "${bundle_url}" | grep -Fq '?v='; then
  echo "Runtime payload BOOTSTRAP_BUNDLE_URL must be a stable Worker URL without ?v= cache busting." >&2
  exit 1
fi

if [ -f "${kickstart_path}" ] && rg -n 'bootstrap-runtime\.env\?v=|ansible-runner-bundle\.tar\.gz\?v=' "${kickstart_path}" >/dev/null 2>&1; then
  echo "Rendered kickstart embeds cache-busted payload URLs; ISO must point at stable Worker URLs." >&2
  exit 1
fi

echo "Runtime payload check passed: recovery export, repo auth, and stable Worker payload contracts are present."
