#!/usr/bin/env bash
set -euo pipefail

# Generate the runtime .env contract for setup and bootstrap. platform.yaml
# owns public cluster/delivery values; this script obtains or reuses only the
# secret material needed by Cloudflare, GitHub, Tailscale, and recovery flows.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
platform_profile="${ADAETUM_PLATFORM_PROFILE:-${repo_root}/platform.yaml}"
out_file="${1:-.env}"
existing_file="${2:-.env}"
vm_out_file="${3:-}"
non_interactive="${NON_INTERACTIVE:-0}"
github_sync="${GITHUB_SYNC:-0}"
github_env="${GITHUB_ENVIRONMENT:-cluster}"
write_vm_env="${WRITE_VM_ENV:-0}"
require_cloudflare_bootstrap="${REQUIRE_CLOUDFLARE_BOOTSTRAP:-0}"
require_tailscale_bootstrap="${REQUIRE_TAILSCALE_BOOTSTRAP:-0}"

have_openssl=0
if command -v openssl >/dev/null 2>&1; then
  have_openssl=1
fi

resolve_python_cmd() {
  if command -v python3 >/dev/null 2>&1; then
    printf 'python3'
    return 0
  fi
  if command -v python >/dev/null 2>&1; then
    printf 'python'
    return 0
  fi
  return 1
}

sanitize_env_value() {
  local value="${1:-}"
  local py_cmd=""
  py_cmd="$(resolve_python_cmd || true)"
  if [ -z "${py_cmd}" ]; then
    printf '%s' "${value}" | tr -d '\000-\010\013\014\016-\037\177' | tr -d '\r\n'
    return 0
  fi
  "${py_cmd}" - <<'PY' "${value}"
import sys
value = sys.argv[1] if len(sys.argv) > 1 else ""
clean = "".join(ch for ch in value if ch >= " " and ch != "\x7f")
print(clean.replace("\r", "").replace("\n", ""), end="")
PY
}

existing_value() {
  local key="$1"
  local value=""
  if [ -f "${existing_file}" ]; then
    value="$(awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${existing_file}")"
  fi
  # run-opinionated-setup passes a temp "existing" file that may omit previously
  # generated Cloudflare/R2 credentials. Fall back to current .env values so
  # reruns reuse existing R2 API credentials instead of creating new tokens.
  if [ -z "${value}" ] && [ "${out_file}" != "${existing_file}" ] && [ -f "${out_file}" ]; then
    value="$(awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${out_file}")"
  fi
  sanitize_env_value "${value}"
}

# Read from the provided existing file only (no fallback to out_file).
# Use this when run-opinionated-setup passes a curated temp existing file and we
# want current run inputs (like KS_BASE_URL) to drive derived defaults.
existing_value_primary() {
  local key="$1"
  local value=""
  if [ -f "${existing_file}" ]; then
    value="$(awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${existing_file}")"
  fi
  sanitize_env_value "${value}"
}

rand_token() {
  # Prefer OpenSSL's CSPRNG. Python remains a fallback for supported machines
  # where OpenSSL is unavailable; an empty value makes the caller fail closed.
  if [ "${have_openssl}" = "1" ]; then
    openssl rand -hex 32
  else
    py_cmd="$(resolve_python_cmd || true)"
    if [ -n "${py_cmd}" ]; then
      "${py_cmd}" - <<'PY'
import base64, hashlib, os, time
seed = os.urandom(64) + str(time.time_ns()).encode("utf-8")
digest = hashlib.sha512(seed).digest()
print(base64.urlsafe_b64encode(digest).decode("ascii").rstrip("=")[:64])
PY
      return 0
    fi
    printf ''
  fi
}

prompt_value() {
  local key="$1"
  local label="$2"
  local default="$3"
  local secret="${4:-0}"
  local value=""

  if [ "${non_interactive}" = "1" ]; then
    printf '%s' "${default}" | tr -d '\r\n'
    return 0
  fi

  if [ "${secret}" = "1" ]; then
    read -r -s -p "${label} [hidden]${default:+ [default set]}: " value
    printf '\n'
  else
    read -r -p "${label}${default:+ [${default}]}: " value
  fi
  if [ -z "${value}" ]; then
    value="${default}"
  fi
  printf '%s' "${value}" | tr -d '\r\n'
}

prompt_yes_no() {
  local label="$1"
  local default="${2:-y}" # y|n
  local answer=""

  if [ "${non_interactive}" = "1" ]; then
    printf '%s' "${default}"
    return 0
  fi

  if [ "${default}" = "y" ]; then
    read -r -p "${label} [Y/n]: " answer
    case "${answer}" in
      ""|y|Y|yes|YES) printf 'y' ;;
      *) printf 'n' ;;
    esac
  else
    read -r -p "${label} [y/N]: " answer
    case "${answer}" in
      y|Y|yes|YES) printf 'y' ;;
      *) printf 'n' ;;
    esac
  fi
}

is_valid_r2_bucket() {
  local value="$1"
  if printf '%s' "${value}" | grep -Eq '^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$'; then
    return 0
  fi
  return 1
}

is_valid_http_url() {
  local value="$1"
  if printf '%s' "${value}" | grep -Eq '^https?://[^[:space:]]+$'; then
    return 0
  fi
  return 1
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

infer_github_repo_url() {
  local repo_path=""
  repo_path="$(infer_gh_repo || true)"
  if [ -z "${repo_path}" ]; then
    return 1
  fi
  printf 'https://github.com/%s.git\n' "${repo_path}"
}

infer_repo_owner_from_url() {
  local repo_url="$1"
  python3 - <<'PY' "${repo_url}"
import sys, urllib.parse
u=(sys.argv[1] if len(sys.argv)>1 else "").strip()
owner=""
try:
  p=urllib.parse.urlparse(u)
  path=(p.path or "").strip("/")
  if path:
    parts=path.split("/")
    if len(parts) >= 2:
      owner=parts[-2]
except Exception:
  owner=""
print((owner or "").strip())
PY
}

infer_github_login_from_token() {
  local token="$1"
  python3 - <<'PY' "${token}"
import json, sys, urllib.request
token = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
if not token:
    raise SystemExit(1)
req = urllib.request.Request(
    "https://api.github.com/user",
    headers={
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "User-Agent": "cluster-generate-env-files",
    },
)
try:
    with urllib.request.urlopen(req, timeout=10) as resp:
        data = json.load(resp)
except Exception:
    raise SystemExit(1)
login = (data.get("login") or "").strip()
if not login:
    raise SystemExit(1)
print(login)
PY
}

github_token_looks_git_capable() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*|ghs_*) return 0 ;;
    *) return 1 ;;
  esac
}

infer_repo_name_from_url() {
  local repo_url="$1"
  python3 - <<'PY' "${repo_url}"
import sys, urllib.parse
u=(sys.argv[1] if len(sys.argv)>1 else "").strip()
name=""
try:
  p=urllib.parse.urlparse(u)
  path=(p.path or "").strip("/")
  if path:
    name=path.split("/")[-1]
except Exception:
  name=""
if name.endswith(".git"):
  name=name[:-4]
print((name or "").strip().lower())
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

ensure_valid_http_url() {
  local value="$1"
  local label="$2"
  local out="${value}"
  if [ "${non_interactive}" = "1" ]; then
    if ! is_valid_http_url "${out}"; then
      echo "Invalid URL for ${label}: ${out}" >&2
      echo "Expected format: http://<host>/ or https://<host>/" >&2
      exit 1
    fi
    printf '%s' "${out}"
    return 0
  fi

  while ! is_valid_http_url "${out}"; do
    echo "Invalid URL: ${out}"
    out="$(prompt_value "${label}" "${label}" "${out}")"
  done
  printf '%s' "${out}"
}

ensure_valid_r2_bucket() {
  local value="$1"
  local label="$2"
  local out="${value}"
  if [ "${non_interactive}" = "1" ]; then
    if ! is_valid_r2_bucket "${out}"; then
      echo "Invalid R2 bucket name for ${label}: ${out}" >&2
      echo "Expected 3-63 chars: lowercase letters, numbers, hyphens; must start/end with alphanumeric." >&2
      exit 1
    fi
    printf '%s' "${out}"
    return 0
  fi

  while ! is_valid_r2_bucket "${out}"; do
    echo "Invalid bucket name: ${out}"
    out="$(prompt_value R2_BUCKET "${label}" "${out}")"
  done
  printf '%s' "${out}"
}

cloudflare_bootstrap_from_pat() {
  local token="$1"
  local account_id="$2"
  local bucket="$3"
  local ks_base_url="$4"
  local existing_key_id="$5"
  local existing_secret="$6"
  local existing_endpoint="$7"
  local rancher_public_domain="$8"
  local rancher_origin_url="$9"
  local rancher_http_host_header="${10}"
  local existing_rancher_tunnel_token="${11}"
  local rancher_tunnel_name="${12}"
  local registry_public_domain="${13}"
  local ingress_public_domains="${14}"
  local ingress_public_domains_cleanup="${15}"
  local ingress_origin_url="${16}"
  local ingress_origin_no_tls_verify="${17}"
  local output=""
  local err_output=""
  local key=""
  local value=""
  local out_account_id=""
  local out_bucket=""
  local out_endpoint=""
  local out_key_id=""
  local out_secret=""
  local out_ks_base_url=""
  local out_rancher_tunnel_token=""
  local out_rancher_tunnel_id=""

  if [ -z "${token}" ]; then
    return 1
  fi
  err_output="$(mktemp)"
  if command -v uv >/dev/null 2>&1; then
    output="$(uv run --with certifi python ./tasks/scripts/bootstrap-cloudflare.py \
      --token "${token}" \
      --account-id "${account_id}" \
      --bucket "${bucket}" \
      --ks-base-url "${ks_base_url}" \
      --rancher-public-domain "${rancher_public_domain}" \
      --rancher-origin-url "${rancher_origin_url}" \
      --rancher-http-host-header "${rancher_http_host_header}" \
      --ingress-public-domains "${ingress_public_domains}" \
      --ingress-public-domains-cleanup "${ingress_public_domains_cleanup}" \
      --ingress-origin-url "${ingress_origin_url}" \
      --ingress-origin-no-tls-verify "${ingress_origin_no_tls_verify}" \
      --cloudflared-tunnel-name "${rancher_tunnel_name}" \
      --existing-rancher-cloudflared-tunnel-token "${existing_rancher_tunnel_token}" \
      --existing-r2-access-key-id "${existing_key_id}" \
      --existing-r2-secret-access-key "${existing_secret}" \
      --existing-r2-endpoint "${existing_endpoint}" 2>"${err_output}" || true)"
  else
    local py_cmd=""
    py_cmd="$(resolve_python_cmd || true)"
    if [ -n "${py_cmd}" ]; then
      output="$("${py_cmd}" ./tasks/scripts/bootstrap-cloudflare.py \
      --token "${token}" \
      --account-id "${account_id}" \
      --bucket "${bucket}" \
      --ks-base-url "${ks_base_url}" \
      --rancher-public-domain "${rancher_public_domain}" \
      --rancher-origin-url "${rancher_origin_url}" \
      --rancher-http-host-header "${rancher_http_host_header}" \
      --ingress-public-domains "${ingress_public_domains}" \
      --ingress-public-domains-cleanup "${ingress_public_domains_cleanup}" \
      --ingress-origin-url "${ingress_origin_url}" \
      --ingress-origin-no-tls-verify "${ingress_origin_no_tls_verify}" \
      --cloudflared-tunnel-name "${rancher_tunnel_name}" \
      --existing-rancher-cloudflared-tunnel-token "${existing_rancher_tunnel_token}" \
      --existing-r2-access-key-id "${existing_key_id}" \
      --existing-r2-secret-access-key "${existing_secret}" \
      --existing-r2-endpoint "${existing_endpoint}" 2>"${err_output}" || true)"
    else
      echo "Cloudflare bootstrap error: no python runtime found (uv/python3/python)." >&2
      rm -f "${err_output}"
      return 1
    fi
  fi

  if [ -z "${output}" ]; then
    if [ -s "${err_output}" ]; then
      echo "Cloudflare bootstrap error: $(tr '\n' ' ' < "${err_output}")" >&2
    fi
    rm -f "${err_output}"
    return 1
  fi
  rm -f "${err_output}"

  while IFS='=' read -r key value; do
    [ -n "${key}" ] || continue
    case "${key}" in
      CLOUDFLARE_ACCOUNT_ID) out_account_id="${value}" ;;
      R2_BUCKET) out_bucket="${value}" ;;
      R2_ENDPOINT) out_endpoint="${value}" ;;
      R2_ACCESS_KEY_ID) out_key_id="${value}" ;;
      R2_SECRET_ACCESS_KEY) out_secret="${value}" ;;
      KS_BASE_URL) out_ks_base_url="${value}" ;;
      RANCHER_CLOUDFLARED_TUNNEL_TOKEN) out_rancher_tunnel_token="${value}" ;;
      RANCHER_CLOUDFLARED_TUNNEL_ID) out_rancher_tunnel_id="${value}" ;;
    esac
  done <<< "${output}"

  if [ -z "${out_account_id}" ] || [ -z "${out_key_id}" ] || [ -z "${out_secret}" ] || [ -z "${out_endpoint}" ]; then
    echo "Cloudflare bootstrap returned incomplete data." >&2
    return 1
  fi

  CLOUDFLARE_ACCOUNT_ID="${out_account_id}"
  [ -n "${out_bucket}" ] && R2_BUCKET="${out_bucket}"
  R2_ENDPOINT="${out_endpoint}"
  R2_ACCESS_KEY_ID="${out_key_id}"
  R2_SECRET_ACCESS_KEY="${out_secret}"
  [ -n "${out_ks_base_url}" ] && KS_BASE_URL="${out_ks_base_url}"
  [ -n "${out_rancher_tunnel_token}" ] && RANCHER_CLOUDFLARED_TUNNEL_TOKEN="${out_rancher_tunnel_token}"
  [ -n "${out_rancher_tunnel_id}" ] && RANCHER_CLOUDFLARED_TUNNEL_ID="${out_rancher_tunnel_id}"
  return 0
}

tailscale_bootstrap_from_inputs() {
  local user_token="$1"
  local tailnet="$2"
  local oauth_client_id="$3"
  local oauth_client_secret="$4"
  local cluster_tag="$5"
  local output=""
  local err_output=""
  local key=""
  local value=""
  local out_oauth_id=""
  local out_oauth_secret=""
  local out_cluster_tag=""
  local out_advertise_tags=""
  local out_authkey=""

  err_output="$(mktemp)"
  if command -v uv >/dev/null 2>&1; then
    output="$(uv run --with certifi python ./tasks/scripts/bootstrap-tailscale.py \
      --user-token "${user_token}" \
      --tailnet "${tailnet}" \
      --oauth-client-id "${oauth_client_id}" \
      --oauth-client-secret "${oauth_client_secret}" \
      --cluster-tag "${cluster_tag}" 2>"${err_output}" || true)"
  else
    local py_cmd=""
    py_cmd="$(resolve_python_cmd || true)"
    if [ -n "${py_cmd}" ]; then
      output="$("${py_cmd}" ./tasks/scripts/bootstrap-tailscale.py \
      --user-token "${user_token}" \
      --tailnet "${tailnet}" \
      --oauth-client-id "${oauth_client_id}" \
      --oauth-client-secret "${oauth_client_secret}" \
      --cluster-tag "${cluster_tag}" 2>"${err_output}" || true)"
    else
      echo "Tailscale bootstrap error: no python runtime found (uv/python3/python)." >&2
      rm -f "${err_output}"
      return 1
    fi
  fi

  if [ -z "${output}" ]; then
    if [ -s "${err_output}" ]; then
      echo "Tailscale bootstrap error:" >&2
      cat "${err_output}" >&2
    fi
    rm -f "${err_output}"
    return 1
  fi
  rm -f "${err_output}"

  while IFS='=' read -r key value; do
    [ -n "${key}" ] || continue
    case "${key}" in
      TAILSCALE_OAUTH_CLIENT_ID) out_oauth_id="${value}" ;;
      TAILSCALE_OAUTH_CLIENT_SECRET) out_oauth_secret="${value}" ;;
      TAILSCALE_CLUSTER_TAG) out_cluster_tag="${value}" ;;
      TAILSCALE_ADVERTISE_TAGS) out_advertise_tags="${value}" ;;
      TAILSCALE_AUTHKEY) out_authkey="${value}" ;;
    esac
  done <<< "${output}"

  [ -n "${out_oauth_id}" ] && TAILSCALE_OAUTH_CLIENT_ID="${out_oauth_id}"
  [ -n "${out_oauth_secret}" ] && TAILSCALE_OAUTH_CLIENT_SECRET="${out_oauth_secret}"
  [ -n "${out_cluster_tag}" ] && TAILSCALE_CLUSTER_TAG="${out_cluster_tag}"
  [ -n "${out_advertise_tags}" ] && TAILSCALE_ADVERTISE_TAGS="${out_advertise_tags}"
  [ -n "${out_authkey}" ] && TAILSCALE_AUTHKEY="${out_authkey}"
  return 0
}

sync_github_secrets() {
  local env_name="$1"
  local synced=0
  local skipped=0
  local failed=0
  local scope="env"
  local env_fallback_noted=0
  local key=""
  local value=""
  local normalized=""
  local err_msg=""
  local rc=0

  ensure_github_api_runner || return 1
  prepare_gh_context
  if ! gh_is_authenticated; then
    echo "GitHub sync requested but GITHUB_SYNC_TOKEN was not provided; skipping."
    return 1
  fi
  if [ -z "${GH_REPO:-}" ]; then
    echo "Unable to determine GitHub repo for secret sync."
    echo "Set GH_REPO=owner/repo."
    return 1
  fi
  ensure_github_environment "${GH_REPO}" "${env_name}"

  while IFS= read -r key; do
    [ -n "${key}" ] || continue
    value="${!key:-}"
    normalized="$(printf '%s' "${value}" | tr -d '\r\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')"
    normalized="$(printf '%s' "${normalized}" | sed -E 's/^"(.*)"$/\1/; s/^\x27(.*)\x27$/\1/')"
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
K3S_COREDNS_FORWARDERS
RANCHER_PUBLIC_DOMAIN
RANCHER_CLOUDFLARED_TUNNEL_TOKEN
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
EOF

  echo "GitHub sync summary (target=${scope}${env_name:+:${env_name}}): synced=${synced}, skipped_empty=${skipped}, failed=${failed}"
  [ "${failed}" -eq 0 ]
}

confirm_overwrite() {
  local target="$1"
  if [ "${non_interactive}" = "1" ]; then
    return 0
  fi
  if [ -f "${target}" ]; then
    local ans=""
    read -r -p "${target} exists. Overwrite? [y/N]: " ans
    case "${ans}" in
      y|Y|yes|YES) ;;
      *) echo "Aborted."; exit 1 ;;
    esac
  fi
}

write_env_file() {
  local target="$1"
  local profile_local_iso="$2"
  local profile_vm_start="$3"

  cat > "${target}" <<EOF
# ---------------------------------------------------------------------------
# Required (setup.md Section 5) - fill these first
# ---------------------------------------------------------------------------
# Cloudflare + R2 + Worker
CLOUDFLARE_ACCOUNT_ID=${CLOUDFLARE_ACCOUNT_ID}
CLOUDFLARE_API_TOKEN=${CLOUDFLARE_API_TOKEN}
R2_ACCESS_KEY_ID=${R2_ACCESS_KEY_ID}
R2_SECRET_ACCESS_KEY=${R2_SECRET_ACCESS_KEY}
R2_ENDPOINT=${R2_ENDPOINT}
R2_BUCKET=${R2_BUCKET}
KS_BASE_URL=${KS_BASE_URL}
KS_SHARED_TOKEN=${KS_SHARED_TOKEN}
KS_UPLOAD_TOKEN=${KS_UPLOAD_TOKEN}

# Tailscale
TAILSCALE_AUTHKEY=${TAILSCALE_AUTHKEY}
TAILSCALE_USER_API_TOKEN=${TAILSCALE_USER_API_TOKEN}
TAILSCALE_OAUTH_CLIENT_ID=${TAILSCALE_OAUTH_CLIENT_ID}
TAILSCALE_OAUTH_CLIENT_SECRET=${TAILSCALE_OAUTH_CLIENT_SECRET}
TAILSCALE_OAUTH_TTL=${TAILSCALE_OAUTH_TTL}
TAILSCALE_DOMAIN=${TAILSCALE_DOMAIN}
TAILSCALE_CLUSTER_TAG=${TAILSCALE_CLUSTER_TAG}
TAILSCALE_ADVERTISE_TAGS=${TAILSCALE_ADVERTISE_TAGS}

# Platform/Kubernetes render inputs
ANSIBLE_RUNNER_IMAGE=${ANSIBLE_RUNNER_IMAGE}
ANSIBLE_RUNNER_REGISTRY_HOST=${ANSIBLE_RUNNER_REGISTRY_HOST}
K3S_COREDNS_FORWARDERS=${K3S_COREDNS_FORWARDERS}
RANCHER_PUBLIC_DOMAIN=${RANCHER_PUBLIC_DOMAIN}
RANCHER_CLOUDFLARED_TUNNEL_TOKEN=${RANCHER_CLOUDFLARED_TUNNEL_TOKEN}
RANCHER_CLOUDFLARED_TUNNEL_ID=${RANCHER_CLOUDFLARED_TUNNEL_ID}
RANCHER_CLOUDFLARED_TUNNEL_NAME=${RANCHER_CLOUDFLARED_TUNNEL_NAME}
REGISTRY_PUBLIC_DOMAIN=${REGISTRY_PUBLIC_DOMAIN}
# Cloudflare Tunnel origin settings (used during setup auto-bootstrap)
RANCHER_CLOUDFLARED_ORIGIN_URL=${RANCHER_CLOUDFLARED_ORIGIN_URL}
RANCHER_CLOUDFLARED_HTTP_HOST_HEADER=${RANCHER_CLOUDFLARED_HTTP_HOST_HEADER}
# Keep blank by default. Public app hostnames should resolve through ingress +
# external-dns unless you intentionally want Cloudflare Tunnel to own them.
CLOUDFLARED_INGRESS_PUBLIC_HOSTS=${CLOUDFLARED_INGRESS_PUBLIC_HOSTS}
INGRESS_CLOUDFLARED_ORIGIN_URL=${INGRESS_CLOUDFLARED_ORIGIN_URL}
INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY=${INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY}

# GitHub/ArgoCD inputs
GITHUB_SYNC_TOKEN=${GITHUB_SYNC_TOKEN}
ARGOCD_GITHUB_REPO_URL=${ARGOCD_GITHUB_REPO_URL}
ARGOCD_GITHUB_REPO_BRANCH=${ARGOCD_GITHUB_REPO_BRANCH}
ARGOCD_GITHUB_USERNAME=${ARGOCD_GITHUB_USERNAME}
ARGOCD_GITHUB_TOKEN=${ARGOCD_GITHUB_TOKEN}
GITEA_SEED_ENABLED=${GITEA_SEED_ENABLED}
GITEA_SEED_SOURCE_REPO_URL=${GITEA_SEED_SOURCE_REPO_URL}
GITEA_SEED_SOURCE_REPO_BRANCH=${GITEA_SEED_SOURCE_REPO_BRANCH}
GITEA_SEED_SOURCE_USERNAME=${GITEA_SEED_SOURCE_USERNAME}
GITEA_SEED_SOURCE_TOKEN=${GITEA_SEED_SOURCE_TOKEN}
GITEA_PUSH_MIRROR_ENABLED=${GITEA_PUSH_MIRROR_ENABLED}
GITEA_PUSH_MIRROR_REPO_URL=${GITEA_PUSH_MIRROR_REPO_URL}
GITEA_PUSH_MIRROR_USERNAME=${GITEA_PUSH_MIRROR_USERNAME}
GITEA_PUSH_MIRROR_TOKEN=${GITEA_PUSH_MIRROR_TOKEN}
GITEA_SEED_TARGET_OWNER=${GITEA_SEED_TARGET_OWNER}
GITEA_SEED_TARGET_REPO=${GITEA_SEED_TARGET_REPO}
GITHUB_APP_ID=${GITHUB_APP_ID}
GITHUB_APP_INSTALLATION_ID=${GITHUB_APP_INSTALLATION_ID}
GITHUB_APP_PRIVATE_KEY_B64=${GITHUB_APP_PRIVATE_KEY_B64}
MONOREPO_GITHUB_APP_ID=${MONOREPO_GITHUB_APP_ID}
MONOREPO_GITHUB_APP_INSTALLATION_ID=${MONOREPO_GITHUB_APP_INSTALLATION_ID}
MONOREPO_GITHUB_APP_PRIVATE_KEY_B64=${MONOREPO_GITHUB_APP_PRIVATE_KEY_B64}

# ---------------------------------------------------------------------------
# Optional (commented out)
# ---------------------------------------------------------------------------
# Planned: separate zone token for cloudflared tunnel DNS automation
# CLOUDFLARE_ZONE_API_TOKEN=

# Optional monorepo convenience defaults
# MONOREPO_GITHUB_REPO_URL=https://github.com/YOUR_ORG/YOUR_REPO.git
# MONOREPO_GITHUB_REPO_BRANCH=master
# MONOREPO_GITHUB_USERNAME=oauth2
# MONOREPO_GITHUB_TOKEN=

# Optional kickstart URLs/overrides
# KS_FILE_URL=\${KS_BASE_URL}/iso/rocky10-server.ks
# KS_LOG_URL=\${KS_BASE_URL}/logs

# Optional ansible runner repo auth
# ANSIBLE_RUNNER_REPO=https://github.com/YOUR_ORG/Infrastructure.git
# ANSIBLE_RUNNER_HTTP_USERNAME=x-access-token
# ANSIBLE_RUNNER_HTTP_TOKEN=
# ANSIBLE_RUNNER_GIT_HOST=github.com
# ANSIBLE_RUNNER_DEPLOY_KEY_B64=

# Optional tailscale behavior
# TAILSCALE_NODE_ROLE=agent
# TAILSCALE_ENFORCE_EXIT_NODE=1
# TAILSCALE_EXIT_NODE=exit-node-1
# TAILSCALE_EXIT_NODE_ALLOW_LAN_ACCESS=0

# ---------------------------------------------------------------------------
# Opinionated local defaults (moved to bottom)
# ---------------------------------------------------------------------------
# Local Embedded Override settings (internal/offline bundling)
LOCAL_ISO_PATH=${profile_local_iso}
LOCAL_KS_TEMPLATE_FILE_NAME=rocky10.ks
ISO_INSTALL_VALIDATE=1
ISO_BUILD_VALIDATE=1
ISO_RESTAMP_CHECKSUM=1
ISO_VALIDATE_REQUIRED=1
ISO_STORAGE_ARGS="inst.nompath rd.multipath=0 inst.nodmraid inst.nomdadm rd.md=0 rd.driver.pre=vmd"
KS_LOG_UPLOAD_DEBUG=1
FIRSTBOOT_NO_POWEROFF=1
BOOTSTRAP_DEBUG_USER=debug
BOOTSTRAP_DEBUG_PASSWORD=
BOOTSTRAP_DEBUG_PASSWORD_HASH=
BOOTSTRAP_DEBUG_SSH_KEY=""
BOOTSTRAP_DEBUG_SUDO=1

# Kickstart behavior
KS_BREAKGLASS=0
KS_LOG_URL=\${KS_BASE_URL}/logs
BOOTSTRAP_DIAGNOSTICS_TAILSCALE=0

# Cluster DNS suffix used for service hostnames
CLUSTER_DOMAIN=${CLUSTER_DOMAIN}
# Local DNS suffix for LAN/VPN-only UI endpoints (derived by default).
CLUSTER_LOCAL_DOMAIN=${CLUSTER_LOCAL_DOMAIN}
# Optional override for the advertised/stable Gitea hostname used by Argo Git.
GITEA_CANONICAL_HOST=
# Optional full override for the advertised/stable Gitea URL used by Argo Git.
GITEA_CANONICAL_URL=

# Bundle/bootstrap behavior
BOOTSTRAP_BUNDLE_URL=\${KS_BASE_URL}/iso/firstboot-payload/ansible-runner-bundle.tar.gz
BOOTSTRAP_BUNDLE_TOKEN=\${KS_SHARED_TOKEN}
BOOTSTRAP_BUNDLE_FORCE=1
BOOTSTRAP_OPENBAO_AUTO_UNSEAL=1
BOOTSTRAP_BACKUP_PASSPHRASE_B64=
BOOTSTRAP_BACKUP_TO_R2=1
BOOTSTRAP_BACKUP_URL=\${KS_BASE_URL}/backup
BOOTSTRAP_BACKUP_FORMAT=both
BOOTSTRAP_BACKUP_FILE_OPENSSL=bootstrap-emergency-kit.tar.gz.enc
BOOTSTRAP_BACKUP_FILE_7Z=bootstrap-emergency-kit.7z
BOOTSTRAP_BACKUP_PASSPHRASE=

# Runner behavior
ANSIBLE_RUNNER_UPDATE_FROM_REPO=1

# Break-glass remote control + VM settings (Taskfile)
BG_HOST=
BG_HOST_AUTO=1
BG_HOSTNAME=
BG_TAILSCALE_TAGS="tag:\${TAILSCALE_CLUSTER_TAG:-cluster} tag:server tag:rke2 tag:rocky10"
BG_TAILSCALE_TAGS_MODE=any
BG_TAILSCALE_NAME_PREFIX=node-
BG_TAILSCALE_REQUIRE_ONLINE=1
BG_SCAN_SUBNETS=
BG_SCAN_TIMEOUT=0.1
BG_HOST_FALLBACK=0
BG_USER=ansible
BG_SSH_PORT=22
BG_PHASES="20 30 40 50 60"
BG_WAIT_TIMEOUT=900
BG_WAIT_INTERVAL=5
BG_LOG_DIR=dist/breakglass-logs
BG_VM_START=${profile_vm_start}

# VM provisioning (UTM or VirtualBox)
VM_PROVIDER=virtualbox
UTMCTL_BIN=/Applications/UTM.app/Contents/MacOS/utmctl
VM_NAME=Rocky-local-test
VM_CPUS=auto
VM_RAM_MB=auto
VM_AUTO_RAM_CAP_MB=24576
VM_RAM_CAP_MB=65536
VM_DISK_GB=120
VM_STORAGE_DIR=dist/vms
VM_NET=nat
VM_BRIDGE_IFACE=en0
VM_HEADLESS=1

# Hook runner selection (auto chooses pre-commit on Windows/non-brew hosts)
HOOKS_RUNNER=auto
EOF
}

confirm_overwrite "${out_file}"
if [ "${write_vm_env}" = "1" ] && [ -n "${vm_out_file}" ] && [ "${vm_out_file}" != "${out_file}" ]; then
  confirm_overwrite "${vm_out_file}"
fi

default_ttl="$(existing_value TAILSCALE_OAUTH_TTL)"
default_ttl="${default_ttl:-1h}"
default_gh_branch="$(existing_value ARGOCD_GITHUB_REPO_BRANCH)"
default_gh_branch="${default_gh_branch:-master}"
default_gh_user="$(existing_value ARGOCD_GITHUB_USERNAME)"
if [ "${default_gh_user}" = "oauth2" ]; then
  default_gh_user=""
fi
default_gh_user="${default_gh_user:-$(infer_github_login_from_token "$(existing_value GITEA_SEED_SOURCE_TOKEN)" || true)}"
default_gh_user="${default_gh_user:-$(infer_github_login_from_token "$(existing_value ARGOCD_GITHUB_TOKEN)" || true)}"
default_gh_user="${default_gh_user:-$(infer_repo_owner_from_url "$(existing_value ARGOCD_GITHUB_REPO_URL)")}"
default_gh_user="${default_gh_user:-$(infer_repo_owner_from_url "$(existing_value MONOREPO_GITHUB_REPO_URL)")}"
default_gh_user="${default_gh_user:-$(infer_repo_owner_from_url "$(infer_github_repo_url || true)")}"
default_gh_user="${default_gh_user:-github-user}"
default_coredns_forwarders="$(existing_value K3S_COREDNS_FORWARDERS)"
default_coredns_forwarders="${default_coredns_forwarders:-8.8.8.8,8.8.4.4}"

printf '\nConfiguring required values for %s\n\n' "${out_file}"

if [ ! -f "${platform_profile}" ]; then
  echo "Missing platform profile: ${platform_profile}" >&2
  exit 1
fi
profile_setup_env="$(mktemp)"
cleanup_profile_setup_env() {
  rm -f "${profile_setup_env}"
}
trap cleanup_profile_setup_env EXIT
python3 "${repo_root}/tasks/scripts/validate-platform-profile.py" --profile "${platform_profile}" >/dev/null
python3 "${repo_root}/tasks/scripts/render-platform-profile.py" --profile "${platform_profile}" --output-setup-env "${profile_setup_env}"
profile_value() {
  local key="$1"
  awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${profile_setup_env}" | tr -d '\r\n'
}
PROFILE_KS_BASE_URL="$(profile_value KS_BASE_URL)"
PROFILE_R2_BUCKET="$(profile_value R2_BUCKET)"
PROFILE_CLUSTER_DOMAIN="$(profile_value CLUSTER_DOMAIN)"
PROFILE_CLUSTER_LOCAL_DOMAIN="$(profile_value CLUSTER_LOCAL_DOMAIN)"
PROFILE_TAILSCALE_DOMAIN="$(profile_value TAILSCALE_DOMAIN)"
PROFILE_TAILSCALE_CLUSTER_TAG="$(profile_value TAILSCALE_CLUSTER_TAG)"
PROFILE_RANCHER_PUBLIC_DOMAIN="$(profile_value RANCHER_PUBLIC_DOMAIN)"
PROFILE_REGISTRY_PUBLIC_DOMAIN="$(profile_value REGISTRY_PUBLIC_DOMAIN)"
PROFILE_GITEA_SEED_TARGET_OWNER="$(profile_value GITEA_SEED_TARGET_OWNER)"
PROFILE_GITEA_SEED_TARGET_REPO="$(profile_value GITEA_SEED_TARGET_REPO)"
PROFILE_ANSIBLE_RUNNER_IMAGE="$(profile_value ANSIBLE_RUNNER_IMAGE)"

# Cloudflare + R2 + Worker
CLOUDFLARE_API_TOKEN="$(prompt_value CLOUDFLARE_API_TOKEN 'CLOUDFLARE_API_TOKEN' "$(existing_value CLOUDFLARE_API_TOKEN)" 1)"

CLOUDFLARE_ACCOUNT_ID="$(existing_value CLOUDFLARE_ACCOUNT_ID)"
R2_ACCESS_KEY_ID="$(existing_value R2_ACCESS_KEY_ID)"
R2_SECRET_ACCESS_KEY="$(existing_value R2_SECRET_ACCESS_KEY)"
R2_ENDPOINT="$(existing_value R2_ENDPOINT)"
R2_BUCKET="${PROFILE_R2_BUCKET}"
KS_BASE_URL="${PROFILE_KS_BASE_URL}"

# Reuse existing R2 credentials without rotating tokens:
# infer account id from endpoint and endpoint from account id when possible.
if [ -z "${CLOUDFLARE_ACCOUNT_ID}" ] && [ -n "${R2_ENDPOINT}" ]; then
  inferred_account_id="$(printf '%s' "${R2_ENDPOINT}" | sed -nE 's#^https://([^.]+)\.r2\.cloudflarestorage\.com/?$#\1#p')"
  if [ -n "${inferred_account_id}" ]; then
    CLOUDFLARE_ACCOUNT_ID="${inferred_account_id}"
  fi
fi
if [ -z "${R2_ENDPOINT}" ] && [ -n "${CLOUDFLARE_ACCOUNT_ID}" ]; then
  R2_ENDPOINT="https://${CLOUDFLARE_ACCOUNT_ID}.r2.cloudflarestorage.com"
fi

if [ -n "${CLOUDFLARE_API_TOKEN}" ]; then
  if [ -z "${CLOUDFLARE_ACCOUNT_ID}" ] || [ -z "${R2_ACCESS_KEY_ID}" ] || [ -z "${R2_SECRET_ACCESS_KEY}" ] || [ -z "${R2_ENDPOINT}" ]; then
    if cloudflare_bootstrap_from_pat \
      "${CLOUDFLARE_API_TOKEN}" \
      "${CLOUDFLARE_ACCOUNT_ID}" \
      "${R2_BUCKET}" \
      "${KS_BASE_URL}" \
      "${R2_ACCESS_KEY_ID}" \
      "${R2_SECRET_ACCESS_KEY}" \
      "${R2_ENDPOINT}" \
      "${PROFILE_RANCHER_PUBLIC_DOMAIN}" \
      "$(existing_value RANCHER_CLOUDFLARED_ORIGIN_URL)" \
      "$(existing_value RANCHER_CLOUDFLARED_HTTP_HOST_HEADER)" \
      "$(existing_value RANCHER_CLOUDFLARED_TUNNEL_TOKEN)" \
      "$(existing_value RANCHER_CLOUDFLARED_TUNNEL_NAME)"; then
      echo "Auto-generated Cloudflare/R2 settings from CLOUDFLARE_API_TOKEN."
    else
      if [ "${require_cloudflare_bootstrap}" = "1" ]; then
        echo "Failed to auto-generate Cloudflare/R2 settings from CLOUDFLARE_API_TOKEN."
        echo "Check token permissions and re-run setup."
        exit 1
      fi
      echo "Warning: could not auto-generate Cloudflare/R2 settings from CLOUDFLARE_API_TOKEN. Falling back to manual prompts."
    fi
  fi
fi

if [ "${require_cloudflare_bootstrap}" = "1" ]; then
  if [ -z "${CLOUDFLARE_ACCOUNT_ID}" ] || [ -z "${R2_ACCESS_KEY_ID}" ] || [ -z "${R2_SECRET_ACCESS_KEY}" ] || [ -z "${R2_ENDPOINT}" ]; then
    echo "Required Cloudflare bootstrap values are missing after auto-generation."
    echo "Expected: CLOUDFLARE_ACCOUNT_ID, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, R2_ENDPOINT."
    echo "Check your CLOUDFLARE_API_TOKEN permissions and account scope."
    exit 1
  fi
fi

CLOUDFLARE_ACCOUNT_ID="$(prompt_value CLOUDFLARE_ACCOUNT_ID 'CLOUDFLARE_ACCOUNT_ID' "${CLOUDFLARE_ACCOUNT_ID}")"
R2_ACCESS_KEY_ID="$(prompt_value R2_ACCESS_KEY_ID 'R2_ACCESS_KEY_ID' "${R2_ACCESS_KEY_ID}")"
R2_SECRET_ACCESS_KEY="$(prompt_value R2_SECRET_ACCESS_KEY 'R2_SECRET_ACCESS_KEY' "${R2_SECRET_ACCESS_KEY}" 1)"
R2_ENDPOINT="$(prompt_value R2_ENDPOINT 'R2_ENDPOINT' "${R2_ENDPOINT}")"
R2_ENDPOINT="$(ensure_valid_http_url "${R2_ENDPOINT}" 'R2_ENDPOINT')"
R2_BUCKET="${PROFILE_R2_BUCKET}"
R2_BUCKET="$(ensure_valid_r2_bucket "${R2_BUCKET}" 'R2_BUCKET')"
KS_BASE_URL="${PROFILE_KS_BASE_URL}"
KS_BASE_URL="$(ensure_valid_http_url "${KS_BASE_URL}" 'KS_BASE_URL')"

CLUSTER_DOMAIN="${PROFILE_CLUSTER_DOMAIN}"
CLUSTER_LOCAL_DOMAIN="${PROFILE_CLUSTER_LOCAL_DOMAIN}"

default_shared="$(existing_value KS_SHARED_TOKEN)"
if [ -z "${default_shared}" ]; then
  default_shared="$(rand_token)"
fi
if [ -z "${default_shared}" ]; then
  echo "Failed to generate KS_SHARED_TOKEN automatically."
  echo "Install openssl or python3, then re-run setup."
  exit 1
fi
default_upload="$(existing_value KS_UPLOAD_TOKEN)"
if [ "${default_upload}" = '${KS_SHARED_TOKEN}' ] || [ "${default_upload}" = '$KS_SHARED_TOKEN' ]; then
  default_upload=""
fi
if [ -z "${default_upload}" ]; then
  default_upload="${default_shared}"
fi
KS_SHARED_TOKEN="${default_shared}"
KS_UPLOAD_TOKEN="${default_upload}"

# Tailscale
tailscale_mode_default="oauth"
if [ -n "$(existing_value TAILSCALE_AUTHKEY)" ]; then
  tailscale_mode_default="authkey"
fi
TAILSCALE_USER_API_TOKEN="$(prompt_value TAILSCALE_USER_API_TOKEN 'TAILSCALE_USER_API_TOKEN (optional fallback)' "$(existing_value TAILSCALE_USER_API_TOKEN)" 1)"
TAILSCALE_MODE="$(prompt_value TAILSCALE_MODE 'Tailscale auth mode (oauth/authkey)' "${tailscale_mode_default}")"
if [ "${require_tailscale_bootstrap}" = "1" ]; then
  TAILSCALE_MODE="oauth"
fi
if [ "${TAILSCALE_MODE}" = "authkey" ]; then
  TAILSCALE_AUTHKEY="$(prompt_value TAILSCALE_AUTHKEY 'TAILSCALE_AUTHKEY' "$(existing_value TAILSCALE_AUTHKEY)" 1)"
  TAILSCALE_OAUTH_CLIENT_ID=""
  TAILSCALE_OAUTH_CLIENT_SECRET=""
else
  TAILSCALE_AUTHKEY=""
  TAILSCALE_OAUTH_CLIENT_ID="$(prompt_value TAILSCALE_OAUTH_CLIENT_ID 'TAILSCALE_OAUTH_CLIENT_ID' "$(existing_value TAILSCALE_OAUTH_CLIENT_ID)")"
  TAILSCALE_OAUTH_CLIENT_SECRET="$(prompt_value TAILSCALE_OAUTH_CLIENT_SECRET 'TAILSCALE_OAUTH_CLIENT_SECRET' "$(existing_value TAILSCALE_OAUTH_CLIENT_SECRET)" 1)"
fi
TAILSCALE_OAUTH_TTL="$(prompt_value TAILSCALE_OAUTH_TTL 'TAILSCALE_OAUTH_TTL' "${default_ttl}")"
TAILSCALE_DOMAIN="${PROFILE_TAILSCALE_DOMAIN}"
TAILSCALE_CLUSTER_TAG="${PROFILE_TAILSCALE_CLUSTER_TAG}"
if [ -z "${TAILSCALE_CLUSTER_TAG}" ]; then
  TAILSCALE_CLUSTER_TAG="tag:cluster"
elif ! printf '%s' "${TAILSCALE_CLUSTER_TAG}" | grep -q '^tag:'; then
  TAILSCALE_CLUSTER_TAG="tag:${TAILSCALE_CLUSTER_TAG}"
fi
TAILSCALE_ADVERTISE_TAGS="$(existing_value TAILSCALE_ADVERTISE_TAGS)"

if [ "${require_tailscale_bootstrap}" = "1" ] && [ -z "${TAILSCALE_DOMAIN}" ]; then
  echo "TAILSCALE_DOMAIN is required for Tailscale ACL/tagOwners validation."
  echo "Set it to your tailnet name (for example: yourcompany.com or yourcompany.ts.net)."
  exit 1
fi

if [ "${TAILSCALE_MODE}" = "oauth" ] || [ "${require_tailscale_bootstrap}" = "1" ]; then
  if tailscale_bootstrap_from_inputs "${TAILSCALE_USER_API_TOKEN}" "${TAILSCALE_DOMAIN}" "${TAILSCALE_OAUTH_CLIENT_ID}" "${TAILSCALE_OAUTH_CLIENT_SECRET}" "${TAILSCALE_CLUSTER_TAG}"; then
    if [ -n "${TAILSCALE_OAUTH_CLIENT_ID:-}" ] && [ -n "${TAILSCALE_OAUTH_CLIENT_SECRET:-}" ]; then
      echo "Validated Tailscale OAuth settings and tag capabilities."
    else
      echo "Validated Tailscale tag policy and bootstrapped TAILSCALE_AUTHKEY via user token."
    fi
  else
    if [ "${require_tailscale_bootstrap}" = "1" ]; then
      echo "Failed to setup/validate Tailscale OAuth bootstrap."
      echo "Provide either:"
      echo "  - valid TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET, OR"
      echo "  - valid TAILSCALE_USER_API_TOKEN for bootstrap."
      echo "OAuth scopes should include: auth_keys, policy_file."
      exit 1
    fi
    echo "Warning: could not validate Tailscale OAuth bootstrap settings."
  fi
fi

# Platform/Kubernetes render inputs
# Opinionated defaults: do not prompt.
ANSIBLE_RUNNER_IMAGE="${PROFILE_ANSIBLE_RUNNER_IMAGE}"
K3S_COREDNS_FORWARDERS="${default_coredns_forwarders}"
RANCHER_PUBLIC_DOMAIN="${PROFILE_RANCHER_PUBLIC_DOMAIN}"
default_rancher_origin_url="$(existing_value RANCHER_CLOUDFLARED_ORIGIN_URL)"
[ -n "${default_rancher_origin_url}" ] || default_rancher_origin_url="http://rancher.cattle-system.svc.cluster.local:80"
RANCHER_CLOUDFLARED_ORIGIN_URL="${default_rancher_origin_url}"
default_rancher_host_header="$(existing_value RANCHER_CLOUDFLARED_HTTP_HOST_HEADER)"
[ -n "${default_rancher_host_header}" ] || default_rancher_host_header="${RANCHER_PUBLIC_DOMAIN}"
RANCHER_CLOUDFLARED_HTTP_HOST_HEADER="${default_rancher_host_header}"
RANCHER_CLOUDFLARED_TUNNEL_TOKEN="$(existing_value RANCHER_CLOUDFLARED_TUNNEL_TOKEN)"
RANCHER_CLOUDFLARED_TUNNEL_ID="$(existing_value RANCHER_CLOUDFLARED_TUNNEL_ID)"
RANCHER_CLOUDFLARED_TUNNEL_NAME="$(existing_value RANCHER_CLOUDFLARED_TUNNEL_NAME)"
REGISTRY_PUBLIC_DOMAIN="${PROFILE_REGISTRY_PUBLIC_DOMAIN}"
default_ingress_public_hosts="$(existing_value_primary CLOUDFLARED_INGRESS_PUBLIC_HOSTS)"
[ -n "${default_ingress_public_hosts}" ] || default_ingress_public_hosts="$(printf '%s' "registry.${CLUSTER_DOMAIN},gitea.${CLUSTER_DOMAIN},argocd.${CLUSTER_DOMAIN},authentik.${CLUSTER_DOMAIN},headlamp.${CLUSTER_DOMAIN},home.${CLUSTER_DOMAIN},alertmanager.${CLUSTER_DOMAIN},grafana.${CLUSTER_DOMAIN},prometheus.${CLUSTER_DOMAIN}")"
CLOUDFLARED_INGRESS_PUBLIC_HOSTS="${default_ingress_public_hosts}"
default_ingress_cleanup_hosts="$(printf '%s' "registry.${CLUSTER_DOMAIN},gitea.${CLUSTER_DOMAIN},argocd.${CLUSTER_DOMAIN},authentik.${CLUSTER_DOMAIN},headlamp.${CLUSTER_DOMAIN},home.${CLUSTER_DOMAIN},alertmanager.${CLUSTER_DOMAIN},grafana.${CLUSTER_DOMAIN},prometheus.${CLUSTER_DOMAIN}")"
INGRESS_CLOUDFLARED_CLEANUP_HOSTS="${default_ingress_cleanup_hosts}"
default_ingress_origin_url="$(existing_value INGRESS_CLOUDFLARED_ORIGIN_URL)"
[ -n "${default_ingress_origin_url}" ] || default_ingress_origin_url="https://rke2-ingress-nginx-controller.kube-system.svc.cluster.local:443"
if [ "${default_ingress_origin_url}" = "http://rke2-ingress-nginx-controller.kube-system.svc.cluster.local:80" ]; then
  default_ingress_origin_url="https://rke2-ingress-nginx-controller.kube-system.svc.cluster.local:443"
fi
INGRESS_CLOUDFLARED_ORIGIN_URL="${default_ingress_origin_url}"
default_ingress_origin_no_tls_verify="$(existing_value INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY)"
[ -n "${default_ingress_origin_no_tls_verify}" ] || default_ingress_origin_no_tls_verify="1"
if [ "${default_ingress_origin_url}" = "https://rke2-ingress-nginx-controller.kube-system.svc.cluster.local:443" ] && [ "${default_ingress_origin_no_tls_verify}" = "0" ]; then
  default_ingress_origin_no_tls_verify="1"
fi
INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY="${default_ingress_origin_no_tls_verify}"
ANSIBLE_RUNNER_REGISTRY_HOST="${REGISTRY_PUBLIC_DOMAIN}"

if [ -n "${CLOUDFLARE_API_TOKEN}" ] && [ -n "${RANCHER_PUBLIC_DOMAIN}" ]; then
  if cloudflare_bootstrap_from_pat \
      "${CLOUDFLARE_API_TOKEN}" \
      "${CLOUDFLARE_ACCOUNT_ID}" \
      "${R2_BUCKET}" \
      "${KS_BASE_URL}" \
      "${R2_ACCESS_KEY_ID}" \
      "${R2_SECRET_ACCESS_KEY}" \
      "${R2_ENDPOINT}" \
      "${RANCHER_PUBLIC_DOMAIN}" \
      "${RANCHER_CLOUDFLARED_ORIGIN_URL}" \
      "${RANCHER_CLOUDFLARED_HTTP_HOST_HEADER}" \
      "${RANCHER_CLOUDFLARED_TUNNEL_TOKEN}" \
      "${RANCHER_CLOUDFLARED_TUNNEL_NAME}" \
      "${REGISTRY_PUBLIC_DOMAIN}" \
      "${CLOUDFLARED_INGRESS_PUBLIC_HOSTS}" \
      "${INGRESS_CLOUDFLARED_CLEANUP_HOSTS}" \
      "${INGRESS_CLOUDFLARED_ORIGIN_URL}" \
      "${INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY}"; then
    echo "Auto-generated/validated Rancher cloudflared tunnel settings from CLOUDFLARE_API_TOKEN."
  else
    echo "Warning: could not auto-generate Rancher cloudflared tunnel token from CLOUDFLARE_API_TOKEN."
    echo "If this persists, verify Cloudflare token scopes include Cloudflare Tunnel Edit + Zone DNS Edit."
  fi
fi

# Break-glass requires an in-cluster cloudflared tunnel token. Do not proceed
# with a blank token, because platform bootstrap would silently skip cloudflared.
if [ -n "${RANCHER_PUBLIC_DOMAIN}" ] && [ -z "${RANCHER_CLOUDFLARED_TUNNEL_TOKEN}" ]; then
  if [ -n "${CLOUDFLARE_API_TOKEN}" ]; then
    if cloudflare_bootstrap_from_pat \
        "${CLOUDFLARE_API_TOKEN}" \
        "${CLOUDFLARE_ACCOUNT_ID}" \
        "${R2_BUCKET}" \
        "${KS_BASE_URL}" \
        "${R2_ACCESS_KEY_ID}" \
        "${R2_SECRET_ACCESS_KEY}" \
        "${R2_ENDPOINT}" \
        "${RANCHER_PUBLIC_DOMAIN}" \
        "${RANCHER_CLOUDFLARED_ORIGIN_URL}" \
        "${RANCHER_CLOUDFLARED_HTTP_HOST_HEADER}" \
        "${RANCHER_CLOUDFLARED_TUNNEL_TOKEN}" \
        "${RANCHER_CLOUDFLARED_TUNNEL_NAME}" \
        "${REGISTRY_PUBLIC_DOMAIN}" \
        "${CLOUDFLARED_INGRESS_PUBLIC_HOSTS}" \
        "${INGRESS_CLOUDFLARED_CLEANUP_HOSTS}" \
        "${INGRESS_CLOUDFLARED_ORIGIN_URL}" \
        "${INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY}"; then
      echo "Validated Rancher cloudflared tunnel token from CLOUDFLARE_API_TOKEN."
    fi
  fi
fi

RANCHER_CLOUDFLARED_TUNNEL_TOKEN="$(prompt_value RANCHER_CLOUDFLARED_TUNNEL_TOKEN 'RANCHER_CLOUDFLARED_TUNNEL_TOKEN' "${RANCHER_CLOUDFLARED_TUNNEL_TOKEN}" 1)"
if [ -z "${RANCHER_CLOUDFLARED_TUNNEL_TOKEN}" ]; then
  echo "Missing required RANCHER_CLOUDFLARED_TUNNEL_TOKEN for break-glass bootstrap."
  echo "Setup should auto-generate this from CLOUDFLARE_API_TOKEN."
  echo "Required Cloudflare scopes: Account Cloudflare Tunnel Edit, Zone DNS Edit."
  exit 1
fi

# GitHub/ArgoCD inputs
GITHUB_SYNC_TOKEN="$(prompt_value GITHUB_SYNC_TOKEN 'GITHUB_SYNC_TOKEN (repo-capable and mirror-write-capable opinionated token)' "$(existing_value GITHUB_SYNC_TOKEN)" 1)"
default_argocd_github_repo_url="$(existing_value ARGOCD_GITHUB_REPO_URL)"
if [ -z "${default_argocd_github_repo_url}" ]; then
  default_argocd_github_repo_url="$(existing_value MONOREPO_GITHUB_REPO_URL)"
fi
if [ -z "${default_argocd_github_repo_url}" ]; then
  default_argocd_github_repo_url="$(infer_github_repo_url || true)"
fi
ARGOCD_GITHUB_REPO_URL="$(prompt_value ARGOCD_GITHUB_REPO_URL 'ARGOCD_GITHUB_REPO_URL' "${default_argocd_github_repo_url}")"
ARGOCD_GITHUB_REPO_BRANCH="$(prompt_value ARGOCD_GITHUB_REPO_BRANCH 'ARGOCD_GITHUB_REPO_BRANCH' "${default_gh_branch}")"

# Gitea seed inputs (source GitHub repo -> target Gitea repo).
GITEA_SEED_ENABLED="$(existing_value GITEA_SEED_ENABLED)"
[ -n "${GITEA_SEED_ENABLED}" ] || GITEA_SEED_ENABLED="1"

default_seed_source_repo="$(existing_value GITEA_SEED_SOURCE_REPO_URL)"
[ -n "${default_seed_source_repo}" ] || default_seed_source_repo="${ARGOCD_GITHUB_REPO_URL}"
GITEA_SEED_SOURCE_REPO_URL="$(prompt_value GITEA_SEED_SOURCE_REPO_URL 'GITEA_SEED_SOURCE_REPO_URL' "${default_seed_source_repo}")"

default_seed_source_branch="$(existing_value GITEA_SEED_SOURCE_REPO_BRANCH)"
[ -n "${default_seed_source_branch}" ] || default_seed_source_branch="${ARGOCD_GITHUB_REPO_BRANCH}"
GITEA_SEED_SOURCE_REPO_BRANCH="$(prompt_value GITEA_SEED_SOURCE_REPO_BRANCH 'GITEA_SEED_SOURCE_REPO_BRANCH' "${default_seed_source_branch}")"

default_seed_source_username="$(existing_value GITEA_SEED_SOURCE_USERNAME)"
[ "${default_seed_source_username}" = "oauth2" ] && default_seed_source_username=""
[ -n "${default_seed_source_username}" ] || default_seed_source_username="$(existing_value ARGOCD_GITHUB_USERNAME)"
[ "${default_seed_source_username}" = "oauth2" ] && default_seed_source_username="${default_gh_user}"
GITEA_SEED_SOURCE_USERNAME="${default_seed_source_username}"

GITEA_SEED_TARGET_OWNER="${PROFILE_GITEA_SEED_TARGET_OWNER}"

GITEA_SEED_TARGET_REPO="${PROFILE_GITEA_SEED_TARGET_REPO}"

gh_auth_mode_default="app"
existing_github_sync_token="$(existing_value GITHUB_SYNC_TOKEN)"
if github_token_looks_git_capable "${existing_github_sync_token}"; then
  gh_auth_mode_default="token"
fi
if [ "${OPINIONATED_GITHUB_TOKEN_MODE:-0}" = "1" ]; then
  if ! github_token_looks_git_capable "${GITHUB_SYNC_TOKEN}"; then
    echo "Opinionated setup requires GITHUB_SYNC_TOKEN to be a git-capable GitHub token." >&2
    echo "Restore or set GITHUB_SYNC_TOKEN; do not rely on ARGOCD_GITHUB_TOKEN or GITEA_SEED_SOURCE_TOKEN in this workflow." >&2
    exit 1
  fi
  GH_AUTH_MODE="token"
else
  if [ -n "$(existing_value ARGOCD_GITHUB_TOKEN)" ]; then
    gh_auth_mode_default="token"
  fi
  if [ -n "$(existing_value GITHUB_APP_ID)" ] || [ -n "$(existing_value GITHUB_APP_PRIVATE_KEY_B64)" ]; then
    gh_auth_mode_default="app"
  fi
  GH_AUTH_MODE="$(prompt_value GH_AUTH_MODE 'GitHub auth mode (app/token)' "${gh_auth_mode_default}")"
fi

if [ "${GH_AUTH_MODE}" = "token" ]; then
  if [ "${OPINIONATED_GITHUB_TOKEN_MODE:-0}" = "1" ]; then
    ARGOCD_GITHUB_USERNAME="${default_gh_user}"
    ARGOCD_GITHUB_TOKEN="${GITHUB_SYNC_TOKEN}"
    GITEA_SEED_SOURCE_USERNAME="${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME}}"
    GITEA_SEED_SOURCE_TOKEN="${GITHUB_SYNC_TOKEN}"
  else
    ARGOCD_GITHUB_USERNAME="$(prompt_value ARGOCD_GITHUB_USERNAME 'ARGOCD_GITHUB_USERNAME' "${default_gh_user}")"
    default_argocd_github_token="$(existing_value ARGOCD_GITHUB_TOKEN)"
    [ -n "${default_argocd_github_token}" ] || default_argocd_github_token="$(existing_value GITHUB_SYNC_TOKEN)"
    ARGOCD_GITHUB_TOKEN="$(prompt_value ARGOCD_GITHUB_TOKEN 'ARGOCD_GITHUB_TOKEN' "${default_argocd_github_token}" 1)"
    GITEA_SEED_SOURCE_USERNAME="${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME}}"
    default_seed_source_token="$(existing_value GITEA_SEED_SOURCE_TOKEN)"
    [ -n "${default_seed_source_token}" ] || default_seed_source_token="${ARGOCD_GITHUB_TOKEN}"
    [ -n "${default_seed_source_token}" ] || default_seed_source_token="$(existing_value GITHUB_SYNC_TOKEN)"
    GITEA_SEED_SOURCE_TOKEN="$(prompt_value GITEA_SEED_SOURCE_TOKEN 'GITEA_SEED_SOURCE_TOKEN' "${default_seed_source_token}" 1)"
    if [ -z "${GITEA_SEED_SOURCE_TOKEN}" ]; then
      GITEA_SEED_SOURCE_TOKEN="${ARGOCD_GITHUB_TOKEN}"
    fi
  fi
  GITHUB_APP_ID=""
  GITHUB_APP_INSTALLATION_ID=""
  GITHUB_APP_PRIVATE_KEY_B64=""
  MONOREPO_GITHUB_APP_ID=""
  MONOREPO_GITHUB_APP_INSTALLATION_ID=""
  MONOREPO_GITHUB_APP_PRIVATE_KEY_B64=""
else
  ARGOCD_GITHUB_USERNAME="$(prompt_value ARGOCD_GITHUB_USERNAME 'ARGOCD_GITHUB_USERNAME' "${default_gh_user}")"
  ARGOCD_GITHUB_TOKEN=""
  GITEA_SEED_SOURCE_USERNAME="${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME}}"
  default_seed_source_token=""
  GITEA_SEED_SOURCE_TOKEN="$(prompt_value GITEA_SEED_SOURCE_TOKEN 'GITEA_SEED_SOURCE_TOKEN (optional override)' "${default_seed_source_token}" 1)"
  GITHUB_APP_ID="$(prompt_value GITHUB_APP_ID 'GITHUB_APP_ID' "$(existing_value GITHUB_APP_ID)")"
  GITHUB_APP_INSTALLATION_ID="$(prompt_value GITHUB_APP_INSTALLATION_ID 'GITHUB_APP_INSTALLATION_ID' "$(existing_value GITHUB_APP_INSTALLATION_ID)")"
  GITHUB_APP_PRIVATE_KEY_B64="$(prompt_value GITHUB_APP_PRIVATE_KEY_B64 'GITHUB_APP_PRIVATE_KEY_B64' "$(existing_value GITHUB_APP_PRIVATE_KEY_B64)" 1)"

  share_app_default="y"
  if [ -n "$(existing_value MONOREPO_GITHUB_APP_ID)" ] && [ "$(existing_value MONOREPO_GITHUB_APP_ID)" != "$(existing_value GITHUB_APP_ID)" ]; then
    share_app_default="n"
  fi
  SHARE_APP_CREDS="$(prompt_yes_no 'Use same app creds for MONOREPO_GITHUB_APP_*?' "${share_app_default}")"
  if [ "${SHARE_APP_CREDS}" = "y" ]; then
    MONOREPO_GITHUB_APP_ID="${GITHUB_APP_ID}"
    MONOREPO_GITHUB_APP_INSTALLATION_ID="${GITHUB_APP_INSTALLATION_ID}"
    MONOREPO_GITHUB_APP_PRIVATE_KEY_B64="${GITHUB_APP_PRIVATE_KEY_B64}"
  else
    MONOREPO_GITHUB_APP_ID="$(prompt_value MONOREPO_GITHUB_APP_ID 'MONOREPO_GITHUB_APP_ID' "$(existing_value MONOREPO_GITHUB_APP_ID)")"
    MONOREPO_GITHUB_APP_INSTALLATION_ID="$(prompt_value MONOREPO_GITHUB_APP_INSTALLATION_ID 'MONOREPO_GITHUB_APP_INSTALLATION_ID' "$(existing_value MONOREPO_GITHUB_APP_INSTALLATION_ID)")"
    MONOREPO_GITHUB_APP_PRIVATE_KEY_B64="$(prompt_value MONOREPO_GITHUB_APP_PRIVATE_KEY_B64 'MONOREPO_GITHUB_APP_PRIVATE_KEY_B64' "$(existing_value MONOREPO_GITHUB_APP_PRIVATE_KEY_B64)" 1)"
  fi
fi

if [ "${OPINIONATED_GITHUB_TOKEN_MODE:-0}" = "1" ] && \
   github_token_looks_git_capable "${GITHUB_SYNC_TOKEN}" && \
   infer_repo_owner_from_url "${GITEA_SEED_SOURCE_REPO_URL:-}" >/dev/null 2>&1; then
  GITEA_PUSH_MIRROR_ENABLED="1"
  GITEA_PUSH_MIRROR_REPO_URL="${GITEA_SEED_SOURCE_REPO_URL}"
  GITEA_PUSH_MIRROR_USERNAME="${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME}}"
  GITEA_PUSH_MIRROR_TOKEN="${GITHUB_SYNC_TOKEN}"
else
  GITEA_PUSH_MIRROR_ENABLED="$(existing_value GITEA_PUSH_MIRROR_ENABLED)"
  [ -n "${GITEA_PUSH_MIRROR_ENABLED}" ] || GITEA_PUSH_MIRROR_ENABLED="1"
  GITEA_PUSH_MIRROR_REPO_URL="$(existing_value GITEA_PUSH_MIRROR_REPO_URL)"
  [ -n "${GITEA_PUSH_MIRROR_REPO_URL}" ] || GITEA_PUSH_MIRROR_REPO_URL="${GITEA_SEED_SOURCE_REPO_URL}"
  GITEA_PUSH_MIRROR_USERNAME="$(existing_value GITEA_PUSH_MIRROR_USERNAME)"
  [ -n "${GITEA_PUSH_MIRROR_USERNAME}" ] || GITEA_PUSH_MIRROR_USERNAME="${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME}}"
  GITEA_PUSH_MIRROR_TOKEN="$(existing_value GITEA_PUSH_MIRROR_TOKEN)"
  [ -n "${GITEA_PUSH_MIRROR_TOKEN}" ] || GITEA_PUSH_MIRROR_TOKEN="${GITEA_SEED_SOURCE_TOKEN:-${ARGOCD_GITHUB_TOKEN}}"
fi

write_env_file "${out_file}" "Rocky-10.1-x86_64-minimal.iso" "0"
echo "Wrote ${out_file}"
if [ "${write_vm_env}" = "1" ] && [ -n "${vm_out_file}" ] && [ "${vm_out_file}" != "${out_file}" ]; then
  write_env_file "${vm_out_file}" "Rocky-10.1-aarch64-minimal.iso" "1"
  echo "Wrote ${vm_out_file}"
fi

if [ "${non_interactive}" = "1" ]; then
  if [ "${github_sync}" = "1" ]; then
    sync_github_secrets "${github_env}" || true
  fi
  exit 0
fi

sync_answer=""
read -r -p "Sync non-empty secrets to GitHub environment ${github_env}? [Y/n]: " sync_answer
case "${sync_answer}" in
  ""|y|Y|yes|YES)
    sync_github_secrets "${github_env}" || true
    ;;
  *)
    echo "Skipped GitHub secret sync."
    ;;
esac
