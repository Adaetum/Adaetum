#!/usr/bin/env bash
set -euo pipefail

# Execute the stateful half of `task initialize` after public profile rendering
# and secret collection. This script may mutate GitHub, Cloudflare, R2, and the
# recovery repository's rendered GitOps files; keep discovery and profile validation outside it.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
# shellcheck source=tasks/scripts/gum-ui.sh
. "${repo_root}/tasks/scripts/gum-ui.sh"
github_env="${GITHUB_ENVIRONMENT:-cluster}"
initial_setup_auto_yes="$(printf '%s' "${INITIAL_SETUP_AUTO_YES:-0}" | tr '[:upper:]' '[:lower:]')"
initial_setup_skip_env="$(printf '%s' "${INITIAL_SETUP_SKIP_ENV_SETUP:-0}" | tr '[:upper:]' '[:lower:]')"
initial_setup_embedded="$(printf '%s' "${INITIAL_SETUP_EMBEDDED:-0}" | tr '[:upper:]' '[:lower:]')"
initial_setup_embedded_prefix="${INITIAL_SETUP_EMBEDDED_PREFIX:-3}"
initial_setup_compact="$(printf '%s' "${INITIAL_SETUP_COMPACT:-0}" | tr '[:upper:]' '[:lower:]')"
initial_setup_detail_log="${INITIAL_SETUP_DETAIL_LOG:-${repo_root}/.adaetum/logs/initial-setup-details.log}"
dry_run="${ADAETUM_INIT_DRY_RUN:-0}"
# Track remote workflow outcomes separately from local preparation so the final
# report can tell an operator exactly which provider-side action needs attention.
workflow_errors=0
triggered_workflow_run_ids=()
triggered_workflow_files=()
bootstrap_bundle_uploaded=0

compact_enabled() {
  [ "${initial_setup_compact}" = "1" ] || [ "${initial_setup_compact}" = "true" ] || [ "${initial_setup_compact}" = "yes" ]
}

run_with_details() {
  local label="$1"
  shift
  if ! compact_enabled; then
    "$@"
    return $?
  fi
  mkdir -p "$(dirname "${initial_setup_detail_log}")"
  if "$@" >>"${initial_setup_detail_log}" 2>&1; then
    status_ok "${label}"
    return 0
  fi
  status_fail "${label} — details: ${initial_setup_detail_log}"
  return 1
}

prompt_yes_no() {
  local label="$1"
  local default="${2:-y}" # y|n
  local answer=""
  if [ "${initial_setup_auto_yes}" = "1" ] || [ "${initial_setup_auto_yes}" = "true" ] || [ "${initial_setup_auto_yes}" = "yes" ]; then
    return 0
  fi
  if adaetum_gum_enabled; then
    adaetum_gum_confirm "${label}" "${default}"
    return $?
  fi
  if [ "${default}" = "y" ]; then
    read -r -p "${label} [Y/n]: " answer
    case "${answer}" in
      ""|y|Y|yes|YES) return 0 ;;
      *) return 1 ;;
    esac
  else
    read -r -p "${label} [y/N]: " answer
    case "${answer}" in
      y|Y|yes|YES) return 0 ;;
      *) return 1 ;;
    esac
  fi
}

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "Missing required command: ${cmd}"
    return 1
  fi
}

task_cmd=""

resolve_task_cmd() {
  if [ -n "${task_cmd}" ]; then
    return 0
  fi
  if command -v task >/dev/null 2>&1; then
    task_cmd="task"
    return 0
  fi
  if command -v task.exe >/dev/null 2>&1; then
    task_cmd="task.exe"
    return 0
  fi
  echo "Missing required command: task (tried: task, task.exe)"
  return 1
}

run_task() {
  resolve_task_cmd || return 1
  "${task_cmd}" "$@"
}

infer_gh_repo() {
  local remote_url=""
  remote_url="$(git config --get remote.origin.url 2>/dev/null || true)"
  if [ -z "${remote_url}" ]; then
    return 1
  fi
  printf '%s\n' "${remote_url}" | sed -E \
    -e 's#^git@github\.com:##' \
    -e 's#^https?://github\.com/##' \
    -e 's#\.git$##'
}

github_token_looks_git_capable() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*|ghs_*) return 0 ;;
    *) return 1 ;;
  esac
}

parse_github_repo_full_name() {
  local repo_url="${1:-}"
  python3 - <<'PY' "${repo_url}"
import re, sys
value = (sys.argv[1] or "").strip()
m = re.match(r'^(?:https?://github\.com/|git@github\.com:)([^/]+)/([^/]+?)(?:\.git)?/?$', value)
if not m:
    sys.exit(1)
print(f"{m.group(1)}/{m.group(2)}")
PY
}

prepare_gh_context() {
  if [ -z "${GH_TOKEN:-}" ]; then
    if [ -n "${GITHUB_SYNC_TOKEN:-}" ]; then
      export GH_TOKEN="${GITHUB_SYNC_TOKEN}"
    fi
  fi

  if [ -z "${GH_REPO:-}" ]; then
    GH_REPO="$(infer_gh_repo || true)"
  fi
}

gh_is_authenticated() {
  [ -n "${GH_TOKEN:-}" ]
}

gh_api_runner=()
gh_api_ready=0

ensure_github_api_runner() {
  # The helper needs PyNaCl to encrypt GitHub environment secrets. Prefer uv so
  # no project-wide Python environment is modified; fall back only when needed.
  if [ "${gh_api_ready}" = "1" ]; then
    return 0
  fi

  if command -v uv >/dev/null 2>&1; then
    gh_api_runner=(uv run --with pynacl python ./tasks/scripts/manage-github-secrets.py)
    gh_api_ready=1
    return 0
  fi

  if ! command -v python3 >/dev/null 2>&1; then
    echo "Missing required runtime: install uv or python3."
    return 1
  fi

  if ! python3 - <<'PY' >/dev/null 2>&1
import nacl  # noqa: F401
PY
  then
    echo "Installing Python dependency: pynacl"
    if ! python3 -m pip install --user pynacl >/dev/null 2>&1; then
      if ! python3 -m pip install pynacl >/dev/null 2>&1; then
        echo "Failed to install pynacl. Install uv or run: python3 -m pip install pynacl"
        return 1
      fi
    fi
  fi

  gh_api_runner=(python3 ./tasks/scripts/manage-github-secrets.py)
  gh_api_ready=1
  return 0
}

run_github_api() {
  ensure_github_api_runner || return 1
  "${gh_api_runner[@]}" "$@"
}

ensure_github_environment() {
  local repo="$1"
  local env_name="$2"
  local err_msg=""
  if err_msg="$(run_github_api ensure-env --repo "${repo}" --env "${env_name}" --token "${GH_TOKEN}" 2>&1)"; then
    return 0
  fi
  echo "Warning: could not ensure GitHub environment '${env_name}' in ${repo}; continuing."
  if [ -n "${err_msg}" ]; then
    echo "GitHub API error: ${err_msg}"
  fi
  return 0
}

env_get_raw_value() {
  local file="$1"
  local key="$2"
  if [ ! -f "${file}" ]; then
    return 1
  fi
  awk -F= -v key="${key}" '$1==key {print substr($0, index($0,"=")+1)}' "${file}" | tail -n1
}

set_env_kv() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp_file=""
  tmp_file="$(mktemp)"
  awk -v key="${key}" -v value="${value}" '
    BEGIN { done=0 }
    $0 ~ ("^" key "=") {
      print key "=" value
      done=1
      next
    }
    { print }
    END {
      if (!done) {
        print key "=" value
      }
    }
  ' "${file}" > "${tmp_file}"
  mv "${tmp_file}" "${file}"
}

update_runtime_env_cache_buster() {
  local env_file="$1"
  local hash_value=""

  if [ ! -f "${env_file}" ]; then
    return 0
  fi

  if [ -f "dist/bootstrap-runtime.env" ]; then
    hash_value="$(sha256sum dist/bootstrap-runtime.env | awk '{print substr($1,1,16)}')"
  else
    hash_value="$(
      python3 - <<'PY' "${env_file}"
import hashlib, pathlib, sys
path = pathlib.Path(sys.argv[1])
lines = []
for raw in path.read_text(encoding="utf-8", errors="replace").splitlines():
    if raw.startswith("BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER="):
        continue
    lines.append(raw)
payload = "\n".join(lines).encode("utf-8")
print(hashlib.sha256(payload).hexdigest()[:16])
PY
    )"
  fi

  if [ -z "${hash_value}" ]; then
    echo "Failed to compute BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER from ${env_file}" >&2
    return 1
  fi

  set_env_kv "${env_file}" "BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER" "${hash_value}"
  export BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER="${hash_value}"
  echo "Updated BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER in ${env_file}."
}

generate_passphrase_from_keyboard_entropy() {
  python3 - <<'PY'
import base64
import hashlib
import os
import sys
import time

seed = bytearray()
count = 0
used_keyboard = False
auto_yes = (os.environ.get("INITIAL_SETUP_AUTO_YES", "").strip().lower() in ("1", "true", "yes", "y"))

if not auto_yes:
    try:
        import termios
        import tty

        tty_stream = open("/dev/tty", "rb", buffering=0)
        fd = tty_stream.fileno()
        old = termios.tcgetattr(fd)
        events = bytearray()
        last = time.monotonic_ns()

        print("Mash random keys for entropy, then press Enter:", file=sys.stderr)
        try:
            tty.setraw(fd)
            while True:
                ch = tty_stream.read(1)
                if not ch:
                    continue
                now = time.monotonic_ns()
                delta = now - last
                last = now
                if ch in (b"\n", b"\r"):
                    break
                count += 1
                events.extend(ch)
                events.extend(delta.to_bytes(8, "big", signed=False))
                if count >= 2048:
                    break
        finally:
            termios.tcsetattr(fd, termios.TCSADRAIN, old)
            tty_stream.close()
            print("", file=sys.stderr)

        if count >= 16:
            seed.extend(events)
            used_keyboard = True
    except Exception:
        pass

if not used_keyboard:
    # Windows/non-TTY fallback: rely on OS CSPRNG + process/time mixing.
    seed.extend(os.urandom(128))

digest = hashlib.sha512(seed).digest()
stream = bytearray()
counter = 0
while len(stream) < 32:
    counter += 1
    stream.extend(hashlib.sha512(digest + counter.to_bytes(4, "big") + seed[:64]).digest())

alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_"
secret = "".join(alphabet[b % len(alphabet)] for b in stream[:28])
parts = [secret[i:i+7] for i in range(0, 28, 7)]
passphrase = "-".join(parts)
passphrase_b64 = base64.b64encode(passphrase.encode("utf-8")).decode("ascii")

print(passphrase)
print(passphrase_b64)
PY
}

ensure_backup_passphrase() {
  local env_file="$1"
  local vm_env_file="$2"
  local plain=""
  local b64=""
  local generated_plain=""
  local generated_b64=""
  local generated_output=""

  if [ ! -f "${env_file}" ]; then
    return 0
  fi

  plain="$(normalize_value "$(env_get_raw_value "${env_file}" "BOOTSTRAP_BACKUP_PASSPHRASE" || true)")"
  b64="$(normalize_value "$(env_get_raw_value "${env_file}" "BOOTSTRAP_BACKUP_PASSPHRASE_B64" || true)")"

  if [ -n "${plain}" ] || [ -n "${b64}" ]; then
    return 0
  fi

  echo "Generating BOOTSTRAP_BACKUP_PASSPHRASE from keyboard entropy..."
  generated_output="$(generate_passphrase_from_keyboard_entropy)"
  generated_plain="$(printf '%s\n' "${generated_output}" | sed -n '1p')"
  generated_b64="$(printf '%s\n' "${generated_output}" | sed -n '2p')"

  if [ -z "${generated_plain}" ] || [ -z "${generated_b64}" ]; then
    echo "Failed to generate backup passphrase."
    return 1
  fi

  set_env_kv "${env_file}" "BOOTSTRAP_BACKUP_PASSPHRASE" "${generated_plain}"
  set_env_kv "${env_file}" "BOOTSTRAP_BACKUP_PASSPHRASE_B64" "${generated_b64}"
  if [ -f "${vm_env_file}" ]; then
    set_env_kv "${vm_env_file}" "BOOTSTRAP_BACKUP_PASSPHRASE" "${generated_plain}"
    set_env_kv "${vm_env_file}" "BOOTSTRAP_BACKUP_PASSPHRASE_B64" "${generated_b64}"
  fi

  echo "Generated and saved BOOTSTRAP_BACKUP_PASSPHRASE in ${env_file}${vm_env_file:+ and ${vm_env_file}}."
}

normalize_value() {
  local value="${1:-}"
  printf '%s' "${value}" | tr -d '\r\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//; s/^\"(.*)\"$/\1/; s/^\x27(.*)\x27$/\1/'
}

is_valid_http_url() {
  local value="$1"
  printf '%s' "${value}" | grep -Eq '^https?://[^[:space:]]+$'
}

is_valid_r2_bucket() {
  local value="$1"
  printf '%s' "${value}" | grep -Eq '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$'
}

default_iso_key_for_file() {
  local file_name="$1"
  case "${file_name}" in
    Rocky-10*.iso) printf 'rocky10/%s' "${file_name}" ;;
    Rocky-9*.iso) printf 'rocky9/%s' "${file_name}" ;;
    ubuntu-24.04*.iso|Ubuntu-24.04*.iso) printf 'ubuntu2404/%s' "${file_name}" ;;
    *) printf '%s' "${file_name}" ;;
  esac
}

local_file_size() {
  local file_path="$1"
  wc -c < "${file_path}" | tr -d '[:space:]'
}

configure_rclone_env() {
  export RCLONE_CONFIG_CLUSTER_R2_TYPE="s3"
  export RCLONE_CONFIG_CLUSTER_R2_PROVIDER="Cloudflare"
  export RCLONE_CONFIG_CLUSTER_R2_ACCESS_KEY_ID="${R2_ACCESS_KEY_ID}"
  export RCLONE_CONFIG_CLUSTER_R2_SECRET_ACCESS_KEY="${R2_SECRET_ACCESS_KEY}"
  export RCLONE_CONFIG_CLUSTER_R2_ENDPOINT="${R2_ENDPOINT}"
  export RCLONE_CONFIG_CLUSTER_R2_NO_CHECK_BUCKET="true"
}

remote_size_for_key() {
  local key="$1"
  local obj_json=""
  if ! obj_json="$(rclone lsjson "cluster_r2:${R2_BUCKET}/${key}" --files-only 2>/dev/null)"; then
    return 1
  fi

  printf '%s' "${obj_json}" | python3 -c 'import json,sys; items=json.load(sys.stdin); print(items[0]["Size"] if items and items[0].get("Size") is not None else "");'
}

upload_iso_if_needed() {
  local local_path="$1"
  local key="$2"
  local local_size=""
  local remote_size=""
  UPLOAD_RESULT="failed"

  local_size="$(local_file_size "${local_path}")"
  if [ -z "${local_size}" ]; then
    echo "Failed to determine size for ${local_path}"
    return 1
  fi

  remote_size="$(remote_size_for_key "${key}" 2>/dev/null || true)"
  if [ -n "${remote_size}" ] && [ "${remote_size}" = "${local_size}" ]; then
    echo "  skip ${local_path} -> s3://${R2_BUCKET}/${key} (size unchanged)"
    UPLOAD_RESULT="skipped"
    return 0
  fi

  echo "  upload ${local_path} -> s3://${R2_BUCKET}/${key}"
  if rclone copyto "${local_path}" "cluster_r2:${R2_BUCKET}/${key}"; then
    UPLOAD_RESULT="uploaded"
    return 0
  fi
  UPLOAD_RESULT="failed"
  return 1
}

load_env() {
  if [ ! -f .env ]; then
    echo "Missing .env"
    return 1
  fi
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a

  CLOUDFLARE_ACCOUNT_ID="$(normalize_value "${CLOUDFLARE_ACCOUNT_ID:-}")"
  CLOUDFLARE_API_TOKEN="$(normalize_value "${CLOUDFLARE_API_TOKEN:-}")"
  R2_ACCESS_KEY_ID="$(normalize_value "${R2_ACCESS_KEY_ID:-}")"
  R2_SECRET_ACCESS_KEY="$(normalize_value "${R2_SECRET_ACCESS_KEY:-}")"
  R2_ENDPOINT="$(normalize_value "${R2_ENDPOINT:-}")"
  R2_BUCKET="$(normalize_value "${R2_BUCKET:-}")"
}

sync_env_to_github() {
  local env_name="${1:-Prod}"
  local key=""
  local value=""
  local normalized=""
  local synced=0
  local skipped=0
  local failed=0
  local scope="env"
  local env_fallback_noted=0
  local err_msg=""
  local rc=0

  ensure_github_api_runner || return 1
  load_env || return 1
  prepare_gh_context
  if ! gh_is_authenticated; then
    echo "No GitHub token provided."
    echo "Set GITHUB_SYNC_TOKEN."
    return 1
  fi
  if [ -z "${GH_REPO:-}" ]; then
    echo "Unable to determine GitHub repo for secret sync."
    echo "Set GH_REPO=owner/repo."
    return 1
  fi
  ensure_github_environment "${GH_REPO}" "${env_name}"

  # Validate key fields before pushing to GitHub.
  normalized="$(normalize_value "${R2_ENDPOINT:-}")"
  if [ -n "${normalized}" ] && ! is_valid_http_url "${normalized}"; then
    echo "Invalid .env R2_ENDPOINT: ${normalized}"
    echo "Expected format: http://<host>/ or https://<host>/"
    return 1
  fi
  normalized="$(normalize_value "${R2_BUCKET:-}")"
  if [ -n "${normalized}" ] && ! is_valid_r2_bucket "${normalized}"; then
    echo "Invalid .env R2_BUCKET: ${normalized}"
    echo "Expected 3-63 chars: lowercase letters, numbers, hyphens; start/end with alphanumeric."
    return 1
  fi

  while IFS= read -r key; do
    [ -n "${key}" ] || continue
    value="${!key:-}"
    normalized="$(normalize_value "${value}")"
    if [ -z "${normalized}" ]; then
      skipped=$((skipped + 1))
      continue
    fi
    if [ "${scope}" = "env" ]; then
      if err_msg="$(run_github_api set-secret --repo "${GH_REPO}" --env "${env_name}" --name "${key}" --value "${normalized}" --token "${GH_TOKEN}" 2>&1)"; then
        rc=0
      else
        rc=$?
      fi
      if [ "${rc}" -eq 0 ]; then
        synced=$((synced + 1))
        continue
      fi
      if [ "${rc}" -eq 4 ] || printf '%s' "${err_msg}" | grep -qiE 'HTTP 404|HTTP 403'; then
        scope="repo"
        if [ "${env_fallback_noted}" -eq 0 ]; then
          echo "Warning: environment secret sync unavailable for ${GH_REPO}/${env_name}; falling back to repo-level secrets."
          env_fallback_noted=1
        fi
        if err_msg="$(run_github_api set-secret --repo "${GH_REPO}" --name "${key}" --value "${normalized}" --token "${GH_TOKEN}" 2>&1)"; then
          rc=0
        else
          rc=$?
        fi
        if [ "${rc}" -eq 0 ]; then
          synced=$((synced + 1))
          continue
        fi
      fi
    else
      if err_msg="$(run_github_api set-secret --repo "${GH_REPO}" --name "${key}" --value "${normalized}" --token "${GH_TOKEN}" 2>&1)"; then
        rc=0
      else
        rc=$?
      fi
      if [ "${rc}" -eq 0 ]; then
        synced=$((synced + 1))
        continue
      fi
    fi

    echo "Failed to set GitHub secret: ${key}"
    if [ "${failed}" -eq 0 ] && [ -n "${err_msg}" ]; then
      echo "GitHub API error: ${err_msg}"
    fi
    failed=$((failed + 1))
  done <<'EOF'
CLOUDFLARE_ACCOUNT_ID
CLOUDFLARE_API_TOKEN
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
R2_ENDPOINT
R2_BUCKET
KS_BASE_URL
KS_SHARED_TOKEN
KS_UPLOAD_TOKEN
CLOUDFLARE_ZONE_API_TOKEN
TAILSCALE_AUTHKEY
TAILSCALE_OAUTH_CLIENT_ID
TAILSCALE_OAUTH_CLIENT_SECRET
TAILSCALE_OAUTH_TTL
TAILSCALE_DOMAIN
TAILSCALE_CLUSTER_TAG
ANSIBLE_RUNNER_IMAGE
ANSIBLE_RUNNER_REGISTRY_HOST
K3S_COREDNS_FORWARDERS
RANCHER_PUBLIC_DOMAIN
RANCHER_CLOUDFLARED_TUNNEL_TOKEN
REGISTRY_PUBLIC_DOMAIN
ARGOCD_GITHUB_REPO_URL
ARGOCD_GITHUB_REPO_BRANCH
ARGOCD_GITHUB_USERNAME
ARGOCD_GITHUB_TOKEN
GITHUB_APP_ID
GITHUB_APP_INSTALLATION_ID
GITHUB_APP_PRIVATE_KEY_B64
MONOREPO_GITHUB_APP_ID
MONOREPO_GITHUB_APP_INSTALLATION_ID
MONOREPO_GITHUB_APP_PRIVATE_KEY_B64
GITEA_SEED_SOURCE_USERNAME
GITEA_SEED_SOURCE_TOKEN
GITEA_PUSH_MIRROR_ENABLED
GITEA_PUSH_MIRROR_REPO_URL
GITEA_PUSH_MIRROR_USERNAME
GITEA_PUSH_MIRROR_TOKEN
BOOTSTRAP_BACKUP_PASSPHRASE
BOOTSTRAP_BACKUP_PASSPHRASE_B64
BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER
EOF

  echo "GitHub sync summary (target=${scope}${env_name:+:${env_name}}): synced=${synced}, skipped_empty=${skipped}, failed=${failed}"
  [ "${failed}" -eq 0 ]
}

upload_golden_isos() {
  require_cmd rclone || return 1
  require_cmd python3 || return 1
  load_env || return 1

  if [ -z "${R2_ENDPOINT:-}" ] || [ -z "${R2_BUCKET:-}" ] || [ -z "${R2_ACCESS_KEY_ID:-}" ] || [ -z "${R2_SECRET_ACCESS_KEY:-}" ]; then
    echo "Missing R2 settings in .env (R2_ENDPOINT, R2_BUCKET, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY)."
    return 1
  fi

  configure_rclone_env

  golden_keys=()
  local_isos=()
  uploaded=0
  skipped=0
  missing=0
  key=""
  iso_path=""
  file_name=""
  matched=0

  if command -v uv >/dev/null 2>&1; then
    uv run --with jinja2 python ./tasks/scripts/compile-kickstarts.py --sync --self-test >/dev/null
  elif python3 -c 'import jinja2' >/dev/null 2>&1; then
    python3 ./tasks/scripts/compile-kickstarts.py --sync --self-test >/dev/null
  else
    echo "Kickstart compilation requires uv or the Python jinja2 package."
    return 1
  fi

  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    golden_keys+=("${line}")
  done <<EOF
$(awk '/^# GOLDEN_ISO_KEY=/{sub("^# GOLDEN_ISO_KEY=",""); print}' dist/ks-templates/*.ks 2>/dev/null | sed '/^$/d' | sort -u)
EOF

  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    local_isos+=("${line}")
  done <<EOF
$(find "${repo_root}" -maxdepth 1 -type f -name '*.iso' | sort)
EOF

  if [ "${#local_isos[@]}" -eq 0 ]; then
    echo "No *.iso files found in repo root (${repo_root})."
    return 1
  fi

  echo "Detected ${#local_isos[@]} root ISO(s). Uploading to s3://${R2_BUCKET}/..."
  for iso_path in "${local_isos[@]}"; do
    file_name="$(basename "${iso_path}")"
    matched=0

    # Bash 3.2 on macOS treats expansion of an empty array as an unbound
    # variable under `set -u`. With no rendered GOLDEN_ISO_KEY entries, use the
    # stable filename-derived key below instead of expanding the empty array.
    if [ "${#golden_keys[@]}" -gt 0 ]; then
      for key in "${golden_keys[@]}"; do
        if [ "$(basename "${key}")" != "${file_name}" ]; then
          continue
        fi
        matched=1
        if upload_iso_if_needed "${iso_path}" "${key}"; then
          case "${UPLOAD_RESULT}" in
            skipped) skipped=$((skipped + 1)) ;;
            uploaded) uploaded=$((uploaded + 1)) ;;
          esac
        else
          missing=$((missing + 1))
        fi
      done
    fi

    if [ "${matched}" -eq 0 ]; then
      key="$(default_iso_key_for_file "${file_name}")"
      if upload_iso_if_needed "${iso_path}" "${key}"; then
        case "${UPLOAD_RESULT}" in
          skipped) skipped=$((skipped + 1)) ;;
          uploaded) uploaded=$((uploaded + 1)) ;;
        esac
      else
        missing=$((missing + 1))
      fi
    fi
  done

  echo "ISO upload summary: uploaded=${uploaded}, skipped_hash_match=${skipped}, failed=${missing}"
  [ "${missing}" -eq 0 ]
}

upload_bootstrap_bundle() {
  resolve_task_cmd || return 1
  if [ ! -f .env ]; then
    echo "Missing .env"
    return 1
  fi

  echo "Publishing ansible bundle and runtime bootstrap payload with live verification..."
  run_task bundle:publish:external
  bootstrap_bundle_uploaded=1
}

workflow_short_name() {
  printf '%s' "$1" | sed -E 's#^.*/##'
}

status_ok() {
  printf '  [ok] %s\n' "$1"
}

status_wait() {
  printf '  [wait] %s\n' "$1"
}

status_fail() {
  printf '  [fail] %s\n' "$1"
}

validate_phase50_source_repo_auth() {
  local env_file="${1:-.env}"
  local source_repo_url=""
  local source_repo_username=""
  local source_repo_token=""
  local github_app_id=""
  local github_app_installation_id=""
  local github_app_private_key_b64=""
  local tmp_dir=""
  local askpass=""
  local err_file=""
  local git_rc=0
  local err_text=""

  source_repo_url="$(env_get_raw_value "${env_file}" "GITEA_SEED_SOURCE_REPO_URL" || true)"
  [ -n "${source_repo_url}" ] || source_repo_url="$(env_get_raw_value "${env_file}" "ARGOCD_GITHUB_REPO_URL" || true)"
  source_repo_username="$(env_get_raw_value "${env_file}" "GITEA_SEED_SOURCE_USERNAME" || true)"
  [ -n "${source_repo_username}" ] || source_repo_username="$(env_get_raw_value "${env_file}" "ARGOCD_GITHUB_USERNAME" || true)"
  source_repo_token="$(env_get_raw_value "${env_file}" "GITEA_SEED_SOURCE_TOKEN" || true)"
  [ -n "${source_repo_token}" ] || source_repo_token="$(env_get_raw_value "${env_file}" "ARGOCD_GITHUB_TOKEN" || true)"
  if [ -z "${source_repo_token}" ]; then
    github_sync_token="$(env_get_raw_value "${env_file}" "GITHUB_SYNC_TOKEN" || true)"
    if github_token_looks_git_capable "${github_sync_token}"; then
      source_repo_token="${github_sync_token}"
    fi
  fi

  github_app_id="$(env_get_raw_value "${env_file}" "GITHUB_APP_ID" || true)"
  github_app_installation_id="$(env_get_raw_value "${env_file}" "GITHUB_APP_INSTALLATION_ID" || true)"
  github_app_private_key_b64="$(env_get_raw_value "${env_file}" "GITHUB_APP_PRIVATE_KEY_B64" || true)"

  if [ -z "${source_repo_url}" ]; then
    echo "Warning: skipping Phase 50 source repo auth validation; source repo URL is not set."
    return 0
  fi

  case "${source_repo_url}" in
    http://*|https://*) ;;
    *)
      echo "Warning: skipping Phase 50 source repo auth validation for non-HTTP source repo URL: ${source_repo_url}"
      return 0
      ;;
  esac

  if [ -z "${source_repo_token}" ]; then
    if [ -n "${github_app_id}" ] && [ -n "${github_app_installation_id}" ] && [ -n "${github_app_private_key_b64}" ]; then
      echo "Warning: skipping direct source repo token probe; Phase 50 will mint a GitHub App installation token at runtime."
      return 0
    fi
    echo "Phase 50 source repo auth is not configured."
    echo "For the opinionated path, set a git-capable GITHUB_SYNC_TOKEN in ${env_file}; the repo-seed fields are derived compatibility outputs."
    echo "For manual/non-opinionated flows, set GitHub App credentials or explicit repo-seed token fields."
    return 1
  fi

  if [ -z "${source_repo_username}" ]; then
    echo "Phase 50 source repo username is not configured."
    echo "Set GITEA_SEED_SOURCE_USERNAME or ARGOCD_GITHUB_USERNAME in ${env_file}."
    return 1
  fi

  tmp_dir="$(mktemp -d)"
  askpass="${tmp_dir}/askpass.sh"
  err_file="${tmp_dir}/ls-remote.err"
  cat >"${askpass}" <<'SH'
#!/bin/sh
case "$1" in
  *sername*) printf '%s\n' "${GIT_ASKPASS_USERNAME:-}" ;;
  *) printf '%s\n' "${GIT_ASKPASS_PASSWORD:-}" ;;
esac
SH
  chmod 0700 "${askpass}"

  GIT_ASKPASS="${askpass}" \
    GIT_ASKPASS_USERNAME="${source_repo_username}" \
    GIT_ASKPASS_PASSWORD="${source_repo_token}" \
    GIT_TERMINAL_PROMPT=0 \
    git -c credential.helper= ls-remote --symref "${source_repo_url}" HEAD >/dev/null 2>"${err_file}" || git_rc=$?

  if [ "${git_rc}" -ne 0 ]; then
    err_text="$(tr -d '\r' < "${err_file}" 2>/dev/null || true)"
    rm -rf "${tmp_dir}" >/dev/null 2>&1 || true
    echo "Phase 50 source repo auth probe failed for ${source_repo_url}."
    if [ -n "${err_text}" ]; then
      printf '%s\n' "${err_text}"
    fi
    echo "Fix the source repo credentials in ${env_file} before continuing."
    return 1
  fi

  rm -rf "${tmp_dir}" >/dev/null 2>&1 || true
  status_ok "Phase 50 source repo auth validated (${source_repo_url})"
  return 0
}

validate_opinionated_github_push_mirror_auth() {
  local env_file="${1:-.env}"
  local sync_token=""
  local mirror_repo_url=""
  local mirror_username=""
  local full_name=""
  local repo_json=""
  local push_allowed=""

  load_env "${env_file}" || return 1
  sync_token="$(env_get_raw_value "${env_file}" "GITHUB_SYNC_TOKEN" || true)"
  if ! github_token_looks_git_capable "${sync_token}"; then
    echo "Opinionated GitHub mirror validation failed: GITHUB_SYNC_TOKEN is missing or not git-capable in ${env_file}." >&2
    return 1
  fi

  mirror_repo_url="$(env_get_raw_value "${env_file}" "GITEA_PUSH_MIRROR_REPO_URL" || true)"
  [ -n "${mirror_repo_url}" ] || mirror_repo_url="$(env_get_raw_value "${env_file}" "GITEA_SEED_SOURCE_REPO_URL" || true)"
  [ -n "${mirror_repo_url}" ] || mirror_repo_url="$(env_get_raw_value "${env_file}" "ARGOCD_GITHUB_REPO_URL" || true)"
  if [ -z "${mirror_repo_url}" ]; then
    echo "Opinionated GitHub mirror validation failed: mirror target repo URL is not configured." >&2
    echo "Restore or set GITHUB_SYNC_TOKEN and let initialize derive the mirror fields." >&2
    return 1
  fi

  full_name="$(parse_github_repo_full_name "${mirror_repo_url}" 2>/dev/null || true)"
  if [ -z "${full_name}" ]; then
    echo "Opinionated GitHub mirror validation failed: mirror target is not a GitHub repo URL: ${mirror_repo_url}" >&2
    return 1
  fi

  mirror_username="$(env_get_raw_value "${env_file}" "GITEA_PUSH_MIRROR_USERNAME" || true)"
  [ -n "${mirror_username}" ] || mirror_username="$(env_get_raw_value "${env_file}" "GITEA_SEED_SOURCE_USERNAME" || true)"
  [ -n "${mirror_username}" ] || mirror_username="$(env_get_raw_value "${env_file}" "ARGOCD_GITHUB_USERNAME" || true)"
  if [ -z "${mirror_username}" ]; then
    echo "Opinionated GitHub mirror validation failed: mirror username is not configured for ${full_name}." >&2
    echo "Restore or set GITHUB_SYNC_TOKEN and rerun initialize so the username is derived." >&2
    return 1
  fi

  repo_json="$(curl -fsS --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer ${sync_token}" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${full_name}" 2>/dev/null)" || {
      echo "Opinionated GitHub mirror validation failed: could not read repo metadata for ${full_name} using GITHUB_SYNC_TOKEN." >&2
      echo "GITHUB_SYNC_TOKEN must have access to clone and mirror-push ${full_name}." >&2
      return 1
    }

  push_allowed="$(printf '%s' "${repo_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); perms=data.get("permissions") or {}; print("true" if perms.get("push") else "false")' 2>/dev/null || true)"
  if [ "${push_allowed}" != "true" ]; then
    echo "Opinionated GitHub mirror validation failed: GITHUB_SYNC_TOKEN does not have push access to ${full_name}." >&2
    echo "GITHUB_SYNC_TOKEN is the canonical opinionated credential and must be write-capable for GitHub push mirroring." >&2
    return 1
  fi

  status_ok "Opinionated GitHub push mirror auth validated (${full_name})"
  return 0
}

ensure_repo_clean_for_workflow_dispatch() {
  local workflow_file="${1:-}"
  local -a pathspecs=()

  if ! command -v git >/dev/null 2>&1; then
    return 0
  fi

  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    return 0
  fi

  case "${workflow_file}" in
    ".github/workflows/ks-worker.yml")
      pathspecs=(
        ".github/workflows/ks-worker.yml"
        ".env.template"
      )
      ;;
    ".github/workflows/ks-publish.yml")
      pathspecs=(
        ".github/workflows/ks-publish.yml"
        "ks-src"
        "tasks/ks.yml"
        "tasks/scripts/compile-kickstarts.py"
        "Taskfile.yml"
        ".env.template"
      )
      ;;
    ".github/workflows/iso-build.yml")
      pathspecs=(
        ".github/workflows/iso-build.yml"
        "ks-src"
        "tasks/ks.yml"
        "tasks/scripts/compile-kickstarts.py"
        "Taskfile.yml"
        ".env.template"
      )
      ;;
  esac

  if [ "${#pathspecs[@]}" -eq 0 ]; then
    if git diff --quiet --exit-code && git diff --cached --quiet --exit-code; then
      return 0
    fi
  elif git diff --quiet --exit-code -- "${pathspecs[@]}" && git diff --cached --quiet --exit-code -- "${pathspecs[@]}"; then
    return 0
  fi

  if compact_enabled; then
    echo "Warning: ${workflow_file} has relevant uncommitted changes; GitHub uses the committed branch."
    if [ "${#pathspecs[@]}" -eq 0 ]; then
      git status --short --untracked-files=no >>"${initial_setup_detail_log}" 2>&1 || true
    else
      git status --short --untracked-files=no -- "${pathspecs[@]}" >>"${initial_setup_detail_log}" 2>&1 || true
    fi
    return 0
  fi
  echo "Warning: detected uncommitted tracked changes relevant to GitHub workflow dispatch."
  echo "GitHub Actions read committed repo state from GitHub, not your local edits."
  if [ "${#pathspecs[@]}" -eq 0 ]; then
    echo "Warning: commit and push these tracked changes before installing from the generated ISO:"
    git status --short --untracked-files=no
  else
    echo "Warning: commit and push the tracked changes relevant to ${workflow_file} before installing from the generated ISO:"
    git status --short --untracked-files=no -- "${pathspecs[@]}"
  fi
  return 0
}

run_workflow() {
  local workflow_file="$1"
  local branch="$2"
  local extra_input="${3:-}"
  local before_ids=""
  local run_id=""
  local new_id=""
  local try=0
  local workflow_encoded=""
  local dispatch_payload=""
  local run_json=""
  local status=""
  local conclusion=""
  local html_url=""
  local poll=0
  local status_line=""
  local wait_for_workflows=""
  local runs_json=""

  wait_for_workflows="$(printf '%s' "${INITIAL_SETUP_WORKFLOW_WAIT:-1}" | tr '[:upper:]' '[:lower:]')"

  prepare_gh_context
  if ! gh_is_authenticated; then
    echo "No GitHub auth available for ${workflow_file}."
    echo "Set GITHUB_SYNC_TOKEN."
    return 1
  fi
  if [ -z "${GH_REPO:-}" ]; then
    echo "Unable to determine GitHub repo for workflow trigger."
    echo "Set GH_REPO=owner/repo."
    return 1
  fi

  require_cmd curl || return 1
  ensure_github_api_runner || return 1

  workflow_encoded="$(python3 - <<'PY' "${workflow_file}"
import sys, urllib.parse
print(urllib.parse.quote(sys.argv[1], safe=""))
PY
)"

  if ! before_ids="$(curl -fsS --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer ${GH_TOKEN}" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${GH_REPO}/actions/workflows/${workflow_encoded}/runs?branch=${branch}&event=workflow_dispatch&per_page=30" \
    | python3 -c 'import json,sys; data=json.load(sys.stdin); [print(run.get("id")) for run in data.get("workflow_runs", []) if run.get("id") is not None]' \
 2>/dev/null | tr '\n' ' ')"; then
    before_ids=""
  fi

  dispatch_payload="$(python3 - <<'PY' "${branch}" "${extra_input}"
import json,sys
ref=sys.argv[1]
extra=sys.argv[2]
inputs={}
if extra:
    if "=" in extra:
        k,v=extra.split("=",1)
        if k:
            inputs[k]=v
    else:
        raise SystemExit("invalid extra input format (expected key=value)")
print(json.dumps({"ref": ref, "inputs": inputs}))
PY
)"

  if ! curl -fsS --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
    -X POST \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer ${GH_TOKEN}" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${GH_REPO}/actions/workflows/${workflow_encoded}/dispatches" \
    -d "${dispatch_payload}" >/dev/null; then
    echo "Failed to trigger workflow via GitHub API: ${workflow_file}"
    return 1
  fi

  run_id=""
  try=0
  while [ "${try}" -lt 30 ]; do
    runs_json="$(curl -fsS --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
      -H "Accept: application/vnd.github+json" \
      -H "Authorization: Bearer ${GH_TOKEN}" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/${GH_REPO}/actions/workflows/${workflow_encoded}/runs?branch=${branch}&event=workflow_dispatch&per_page=30" 2>/dev/null || true)"
    if [ -n "${runs_json}" ]; then
      new_id="$(printf '%s' "${runs_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); before=set(sys.argv[1].split()); rid=""; runs=data.get("workflow_runs", []); 
for run in runs:
    v=str(run.get("id",""))
    if v and v != "None" and v not in before:
        rid=v
        break
print(rid)' "${before_ids}" 2>/dev/null || true)"
    else
      new_id=""
    fi
    if [ -n "${new_id}" ] && [ "${new_id}" != "null" ]; then
      run_id="${new_id}"
      break
    fi
    try=$((try + 1))
    sleep 2
  done

  if [ "${wait_for_workflows}" = "0" ] || [ "${wait_for_workflows}" = "false" ] || [ "${wait_for_workflows}" = "no" ]; then
    if [ -n "${run_id}" ] && [ "${run_id}" != "null" ]; then
      triggered_workflow_run_ids+=("${run_id}")
      triggered_workflow_files+=("${workflow_file}")
      status_ok "$(workflow_short_name "${workflow_file}") triggered (run ${run_id}; deferred validation)"
      return 0
    fi
    status_fail "$(workflow_short_name "${workflow_file}") triggered but run id could not be determined"
    return 1
  fi

  if [ -n "${run_id}" ] && [ "${run_id}" != "null" ]; then
    status_wait "$(workflow_short_name "${workflow_file}") run ${run_id} (live wait)"
    poll=0
    while [ "${poll}" -lt 180 ]; do
      run_json="$(curl -fsS \
        --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer ${GH_TOKEN}" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "https://api.github.com/repos/${GH_REPO}/actions/runs/${run_id}" 2>/dev/null || true)"
      if [ -z "${run_json}" ]; then
        poll=$((poll + 1))
        sleep 5
        continue
      fi
      status="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("status",""))')"
      conclusion="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("conclusion",""))')"
      html_url="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("html_url",""))')"
      if [ "${status}" = "completed" ]; then
        if [ "${conclusion}" = "success" ]; then
          status_ok "$(workflow_short_name "${workflow_file}") succeeded (${html_url})"
          return 0
        fi
        status_fail "$(workflow_short_name "${workflow_file}") failed (conclusion=${conclusion})"
        [ -n "${html_url}" ] && echo "Run URL: ${html_url}"
        return 1
      fi
      if [ $((poll % 6)) -eq 0 ]; then
        status_line="status=${status:-unknown}"
        [ -n "${conclusion}" ] && status_line="${status_line}, conclusion=${conclusion}"
        status_wait "$(workflow_short_name "${workflow_file}") run ${run_id} - ${status_line}"
      fi
      poll=$((poll + 1))
      sleep 5
    done
    status_fail "$(workflow_short_name "${workflow_file}") timed out (run ${run_id})"
    return 1
  else
    status_fail "$(workflow_short_name "${workflow_file}") triggered but run id was not found"
    return 0
  fi
}

run_workflow_step() {
  local workflow_file="$1"
  local branch="$2"
  local extra_input="${3:-}"
  if compact_enabled; then
    if run_workflow "${workflow_file}" "${branch}" "${extra_input}" >>"${initial_setup_detail_log}" 2>&1; then
      status_ok "$(workflow_short_name "${workflow_file}") triggered"
      return 0
    fi
    status_fail "$(workflow_short_name "${workflow_file}") could not be triggered — details: ${initial_setup_detail_log}"
    workflow_errors=1
    return 0
  fi
  if ! run_workflow "${workflow_file}" "${branch}" "${extra_input}"; then
    echo "Warning: workflow step failed for ${workflow_file}; continuing to collect failures."
    workflow_errors=1
  fi
  return 0
}

validate_deferred_workflows() {
  local i=0
  local run_id=""
  local workflow_file=""
  local run_json=""
  local status=""
  local conclusion=""
  local html_url=""
  local poll=0
  local status_line=""
  local workflow_name=""

  if [ "${#triggered_workflow_run_ids[@]}" -eq 0 ]; then
    return 0
  fi

  echo "Validating deferred workflow runs..."
  while [ "${i}" -lt "${#triggered_workflow_run_ids[@]}" ]; do
    run_id="${triggered_workflow_run_ids[$i]}"
    workflow_file="${triggered_workflow_files[$i]}"
    workflow_name="$(workflow_short_name "${workflow_file}")"
    status_wait "${workflow_name} run ${run_id}"
    poll=0
    while [ "${poll}" -lt 180 ]; do
      run_json="$(curl -fsS \
        --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer ${GH_TOKEN}" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "https://api.github.com/repos/${GH_REPO}/actions/runs/${run_id}" 2>/dev/null || true)"
      if [ -z "${run_json}" ]; then
        poll=$((poll + 1))
        sleep 5
        continue
      fi
      status="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("status",""))')"
      conclusion="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("conclusion",""))')"
      html_url="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("html_url",""))')"
      if [ "${status}" = "completed" ]; then
        if [ "${conclusion}" = "success" ]; then
          status_ok "${workflow_name} succeeded (${html_url})"
        else
          status_fail "${workflow_name} failed (conclusion=${conclusion})"
          [ -n "${html_url}" ] && echo "Run URL: ${html_url}"
          if [ "${workflow_name}" = "ks-worker.yml" ] && [ "${bootstrap_bundle_uploaded}" -eq 1 ]; then
            echo "Warning: ks-worker.yml failed, but the break-glass bundle was already uploaded directly in step 4.2."
            echo "Fix the workflow separately and re-run later if you need to redeploy the Worker itself."
          else
            workflow_errors=1
          fi
        fi
        break
      fi
      if [ $((poll % 6)) -eq 0 ]; then
        status_line="status=${status:-unknown}"
        [ -n "${conclusion}" ] && status_line="${status_line}, conclusion=${conclusion}"
        status_wait "${workflow_name} run ${run_id} - ${status_line}"
      fi
      poll=$((poll + 1))
      sleep 5
    done
    if [ "${poll}" -ge 180 ]; then
      status_fail "${workflow_name} timed out (run ${run_id})"
      if [ "${workflow_name}" = "ks-worker.yml" ] && [ "${bootstrap_bundle_uploaded}" -eq 1 ]; then
        echo "Warning: ks-worker.yml timed out, but the break-glass bundle was already uploaded directly in step 4.2."
        echo "Fix the workflow separately and re-run later if you need to redeploy the Worker itself."
      else
        workflow_errors=1
      fi
    fi
    i=$((i + 1))
  done
}

stage() {
  local idx="$1"
  local label="$2"
  if [ "${initial_setup_embedded}" = "1" ] || [ "${initial_setup_embedded}" = "true" ] || [ "${initial_setup_embedded}" = "yes" ]; then
    idx="${idx%%/*}"
    adaetum_ui_task "${initial_setup_embedded_prefix}.${idx}" "${label}"
    return 0
  fi
  adaetum_ui_heading "Stage ${1}: ${2}"
}

sub_stage() {
  local idx="$1"
  local label="$2"
  if [ "${initial_setup_embedded}" = "1" ] || [ "${initial_setup_embedded}" = "true" ] || [ "${initial_setup_embedded}" = "yes" ]; then
    adaetum_ui_subtask "${initial_setup_embedded_prefix}.${idx}" "${label}"
    return 0
  fi
  adaetum_ui_subtask "$1" "$2"
}

if [ "${initial_setup_embedded}" = "1" ] || [ "${initial_setup_embedded}" = "true" ] || [ "${initial_setup_embedded}" = "yes" ]; then
  :
else
  adaetum_ui_hero "ADAETUM  /  BOOTSTRAP" "Initial setup wizard" "Prepare, upload, sync, and publish the first install"
  adaetum_ui_message 245 "Checkout: ${repo_root}"
fi

if [ "${dry_run}" = "1" ]; then
  # The stateful bootstrap phase is one execution boundary. Keep its normal
  # stage/sub-stage rendering, but make every upload, secret-sync, workflow,
  # and ISO action a successful no-op for the shared dry-run control flow.
  stage "1/8" "Environment preparation"
  sub_stage "1.1" "Use existing .env or run env wizard"
  stage "2/8" "Backup passphrase"
  sub_stage "2.1" "Ensure BOOTSTRAP_BACKUP_PASSPHRASE values exist"
  stage "3/8" "Golden ISO upload"
  sub_stage "3.1" "Upload local root ISOs to R2"
  stage "4/8" "Break-glass bundle upload"
  sub_stage "4.0" "Validate Phase 50 source repo auth"
  sub_stage "4.0b" "Validate opinionated GitHub push mirror auth"
  sub_stage "4.1" "Build ansible-runner bundle"
  sub_stage "4.2" "Upload bundle to R2/Worker"
  stage "5/8" "GitHub secret sync"
  sub_stage "5.1" "Sync non-empty .env values to GitHub secrets"
  stage "6/8" "Kickstart worker workflow"
  sub_stage "6.1" "Trigger ks-worker.yml"
  stage "7/8" "Kickstart publish workflow"
  sub_stage "7.1" "Trigger ks-publish.yml"
  stage "8/8" "ISO workflows and local ISO build"
  sub_stage "8.1" "Trigger iso-build.yml"
  sub_stage "8.2" "Build local install ISO (task build-iso)"
  exit 0
fi

stage "1/8" "Environment preparation"
sub_stage "1.1" "Use existing .env or run env wizard"
if [ "${initial_setup_skip_env}" = "1" ] || [ "${initial_setup_skip_env}" = "true" ] || [ "${initial_setup_skip_env}" = "yes" ]; then
  :
elif prompt_yes_no "Run env wizard first (writes .env)?" "y"; then
  bash ./tasks/scripts/generate-env-files.sh .env .env
fi

stage "2/8" "Backup passphrase"
sub_stage "2.1" "Ensure BOOTSTRAP_BACKUP_PASSPHRASE values exist"
run_with_details "Recovery passphrase ready" ensure_backup_passphrase ".env" ""
run_with_details "Runtime version updated" update_runtime_env_cache_buster ".env"

stage "3/8" "Golden ISO upload"
sub_stage "3.1" "Upload local root ISOs to R2"
if prompt_yes_no "Upload golden ISOs from local files to Cloudflare R2 now?" "y"; then
  if ! run_with_details "Installer media uploaded to R2" upload_golden_isos; then
    echo "Warning: installer upload failed; continuing. Details: ${initial_setup_detail_log}"
  fi
fi

stage "4/8" "Break-glass bundle upload"
sub_stage "4.0" "Validate Phase 50 source repo auth"
run_with_details "Source repository access validated" validate_phase50_source_repo_auth ".env"
sub_stage "4.0b" "Validate opinionated GitHub push mirror auth"
run_with_details "Recovery mirror access validated" validate_opinionated_github_push_mirror_auth ".env"
sub_stage "4.1" "Build ansible-runner bundle"
sub_stage "4.2" "Upload bundle to R2/Worker"
bundle_upload_mode="$(printf '%s' "${INITIAL_SETUP_FORCE_BUNDLE_UPLOAD:-1}" | tr '[:upper:]' '[:lower:]')"
if [ "${bundle_upload_mode}" = "1" ] || [ "${bundle_upload_mode}" = "true" ] || [ "${bundle_upload_mode}" = "yes" ]; then
  if ! run_with_details "Break-glass bundle built and published" upload_bootstrap_bundle; then
    echo "Warning: break-glass bundle publication failed; continuing. Details: ${initial_setup_detail_log}"
  fi
elif prompt_yes_no "Build and upload break-glass ansible bundle now?" "y"; then
  if ! run_with_details "Break-glass bundle built and published" upload_bootstrap_bundle; then
    echo "Warning: break-glass bundle publication failed; continuing. Details: ${initial_setup_detail_log}"
  fi
fi
if [ -f "dist/bootstrap-runtime.env" ]; then
  run_with_details "Runtime version finalized" update_runtime_env_cache_buster ".env"
fi

stage "5/8" "GitHub secret sync"
sub_stage "5.1" "Sync non-empty .env values to GitHub secrets"
if prompt_yes_no "Sync current .env non-empty secrets to GitHub ${github_env} now?" "y"; then
  if ! run_with_details "GitHub environment secrets synchronized" sync_env_to_github "${github_env}"; then
    echo "Warning: GitHub secret sync failed; continuing. Details: ${initial_setup_detail_log}"
  fi
fi

branch="$(git branch --show-current)"

stage "6/8" "Kickstart worker workflow"
sub_stage "6.1" "Trigger ks-worker.yml"
ensure_repo_clean_for_workflow_dispatch ".github/workflows/ks-worker.yml"
if prompt_yes_no "Trigger ks-worker workflow now?" "y"; then
  run_workflow_step ".github/workflows/ks-worker.yml" "${branch}" ""
fi

stage "7/8" "Kickstart publish workflow"
sub_stage "7.1" "Trigger ks-publish.yml"
ensure_repo_clean_for_workflow_dispatch ".github/workflows/ks-publish.yml"
if prompt_yes_no "Trigger ks-publish workflow now?" "y"; then
  run_workflow_step ".github/workflows/ks-publish.yml" "${branch}" ""
fi

stage "8/8" "ISO workflows and local ISO build"
sub_stage "8.1" "Trigger iso-build.yml"
ensure_repo_clean_for_workflow_dispatch ".github/workflows/iso-build.yml"
if prompt_yes_no "Trigger iso-build workflow now?" "y"; then
  ks_input="${INITIAL_SETUP_KS_TEMPLATE_FILE_NAME:-}"
  if [ "${initial_setup_auto_yes}" != "1" ] && [ "${initial_setup_auto_yes}" != "true" ] && [ "${initial_setup_auto_yes}" != "yes" ]; then
    if adaetum_gum_enabled; then
      ks_input="$(adaetum_gum_input "Optional ks_template_file_name (blank for all)" "" 0)" || exit 1
    else
      read -r -p "Optional ks_template_file_name (blank for all): " ks_input
    fi
  fi
  wf_input=""
  if [ -n "${ks_input}" ]; then
    wf_input="ks_template_file_name=${ks_input}"
  fi
  run_workflow_step ".github/workflows/iso-build.yml" "${branch}" "${wf_input}"
fi

wait_for_workflows="$(printf '%s' "${INITIAL_SETUP_WORKFLOW_WAIT:-1}" | tr '[:upper:]' '[:lower:]')"
deferred_mode=0
if [ "${wait_for_workflows}" = "0" ] || [ "${wait_for_workflows}" = "false" ] || [ "${wait_for_workflows}" = "no" ]; then
  deferred_mode=1
fi

sub_stage "8.2" "Build local install ISO (task build-iso)"
run_local_iso_setting="$(printf '%s' "${INITIAL_SETUP_RUN_LOCAL_ISO_BUILD:-prompt}" | tr '[:upper:]' '[:lower:]')"
local_iso_requested=0
local_iso_bg_pid=""
local_iso_bg_log="${repo_root}/.adaetum/logs/local-iso-build.log"
case "${run_local_iso_setting}" in
  1|true|yes|y)
    local_iso_requested=1
    ;;
  0|false|no|n)
    echo "Skipping local ISO build (INITIAL_SETUP_RUN_LOCAL_ISO_BUILD=${INITIAL_SETUP_RUN_LOCAL_ISO_BUILD})."
    ;;
  *)
    if prompt_yes_no "Run local ISO build now (task build-iso)?" "n"; then
      local_iso_requested=1
    fi
    ;;
esac

  if [ "${local_iso_requested}" -eq 1 ]; then
  if [ "${deferred_mode}" -eq 1 ]; then
    mkdir -p "$(dirname "${local_iso_bg_log}")"
    rm -f "${local_iso_bg_log}"
    echo "Starting local ISO build in background..."
    (run_task build-iso >"${local_iso_bg_log}" 2>&1) &
    local_iso_bg_pid="$!"
    echo "Local ISO build running (pid=${local_iso_bg_pid})."
  else
    if ! run_task build-iso; then
      echo "Warning: local ISO build failed."
    fi
  fi
fi

if [ "${deferred_mode}" -eq 1 ]; then
  sub_stage "8.3" "Validate deferred workflow completion"
  validate_deferred_workflows
fi

if [ -n "${local_iso_bg_pid}" ]; then
  sub_stage "8.4" "Finalize local ISO build"
  if wait "${local_iso_bg_pid}"; then
    echo "Local ISO build completed successfully."
    rm -f "${local_iso_bg_log}" 2>/dev/null || true
  else
    echo "Warning: local ISO build failed."
    if [ -f "${local_iso_bg_log}" ]; then
      if compact_enabled; then
        echo "Details: ${local_iso_bg_log}"
      else
        echo "Local ISO build log tail (last 60 lines):"
        tail -n 60 "${local_iso_bg_log}" || true
      fi
    fi
  fi
fi

if [ "${workflow_errors}" -ne 0 ]; then
  echo "Wizard failed: one or more workflow runs did not complete successfully."
  exit 1
fi

echo "Wizard complete."
echo "Phase 99 command (uses generated .env passphrase):"
echo "  sudo BOOTSTRAP_BACKUP_PASSPHRASE=\"\$(awk -F= '/^BOOTSTRAP_BACKUP_PASSPHRASE=/{print substr(\$0,index(\$0,\"=\")+1)}' /opt/ansible-runner/.env | tail -n1)\" /opt/ansible-runner/ansible/ansible-scripts/bootstrap/Phase-90/run-phase99.sh"
echo "Alternative:"
echo "  sudo bash -lc 'set -a; source /opt/ansible-runner/.env 2>/dev/null || true; set +a; /opt/ansible-runner/ansible/ansible-scripts/bootstrap/Phase-90/run-phase99.sh'"
