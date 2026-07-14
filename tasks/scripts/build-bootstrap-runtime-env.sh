#!/usr/bin/env bash
set -euo pipefail

# Build the secret-bearing runtime payload embedded in the break-glass bundle.
# It is derived from local runtime inputs, never from platform.yaml secrets
# (which are prohibited), and is validated before the installer consumes it.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

ks_env="${KS_ENV:-.env}"
out_path="${1:-dist/bootstrap-runtime.env}"

if [ -f "${ks_env}" ]; then
  set -a
  # shellcheck disable=SC1090
  . "${ks_env}"
  set +a
elif [ "${ks_env}" != ".env" ] || [ ! -f ".env" ]; then
  echo "KS env file not found (${ks_env}); building runtime payload from current process environment." >&2
fi

MONOREPO_GITHUB_REPO_URL="${MONOREPO_GITHUB_REPO_URL:-}"
MONOREPO_GITHUB_REPO_BRANCH="${MONOREPO_GITHUB_REPO_BRANCH:-master}"
MONOREPO_GITHUB_USERNAME="${MONOREPO_GITHUB_USERNAME:-}"
MONOREPO_GITHUB_TOKEN="${MONOREPO_GITHUB_TOKEN:-}"
MONOREPO_GITHUB_HOST="${MONOREPO_GITHUB_HOST:-}"
GITHUB_SYNC_TOKEN="${GITHUB_SYNC_TOKEN:-}"
GITHUB_APP_ID="${GITHUB_APP_ID:-${MONOREPO_GITHUB_APP_ID:-}}"
GITHUB_APP_INSTALLATION_ID="${GITHUB_APP_INSTALLATION_ID:-${MONOREPO_GITHUB_APP_INSTALLATION_ID:-}}"
GITHUB_APP_PRIVATE_KEY_B64="${GITHUB_APP_PRIVATE_KEY_B64:-${MONOREPO_GITHUB_APP_PRIVATE_KEY_B64:-}}"

github_token_looks_git_capable() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*|ghs_*) return 0 ;;
    *) return 1 ;;
  esac
}

# GITHUB_SYNC_TOKEN is the canonical opinionated credential. The aliases below
# exist only as generated runtime fields consumed by Argo CD and Gitea during
# the initial seed; users should not manage them independently.
repo_auth_fallback_token=""
if github_token_looks_git_capable "${GITHUB_SYNC_TOKEN}"; then
  repo_auth_fallback_token="${GITHUB_SYNC_TOKEN}"
elif github_token_looks_git_capable "${MONOREPO_GITHUB_TOKEN}"; then
  repo_auth_fallback_token="${MONOREPO_GITHUB_TOKEN}"
fi

ARGOCD_GITHUB_REPO_URL="${ARGOCD_GITHUB_REPO_URL:-${MONOREPO_GITHUB_REPO_URL}}"
ARGOCD_GITHUB_REPO_BRANCH="${ARGOCD_GITHUB_REPO_BRANCH:-${MONOREPO_GITHUB_REPO_BRANCH}}"
ARGOCD_GITHUB_USERNAME="${ARGOCD_GITHUB_USERNAME:-${MONOREPO_GITHUB_USERNAME}}"
if [ -n "${repo_auth_fallback_token}" ]; then
  ARGOCD_GITHUB_TOKEN="${repo_auth_fallback_token}"
else
  ARGOCD_GITHUB_TOKEN="${ARGOCD_GITHUB_TOKEN:-}"
fi
if [ -n "${ARGOCD_GITHUB_TOKEN}" ] && [ -z "${ARGOCD_GITHUB_USERNAME}" ]; then
  ARGOCD_GITHUB_USERNAME="oauth2"
fi

ANSIBLE_RUNNER_REPO="${ANSIBLE_RUNNER_REPO:-${MONOREPO_GITHUB_REPO_URL}}"
ANSIBLE_RUNNER_GIT_HOST="${ANSIBLE_RUNNER_GIT_HOST:-${MONOREPO_GITHUB_HOST}}"
ANSIBLE_RUNNER_HTTP_TOKEN="${ANSIBLE_RUNNER_HTTP_TOKEN:-${GITHUB_SYNC_TOKEN:-${MONOREPO_GITHUB_TOKEN:-}}}"

GITEA_SEED_SOURCE_REPO_URL="${GITEA_SEED_SOURCE_REPO_URL:-${ARGOCD_GITHUB_REPO_URL:-}}"
GITEA_SEED_SOURCE_REPO_BRANCH="${GITEA_SEED_SOURCE_REPO_BRANCH:-${ARGOCD_GITHUB_REPO_BRANCH:-master}}"
if [ -n "${repo_auth_fallback_token}" ]; then
  GITEA_SEED_SOURCE_TOKEN="${repo_auth_fallback_token}"
else
  GITEA_SEED_SOURCE_TOKEN="${GITEA_SEED_SOURCE_TOKEN:-${ARGOCD_GITHUB_TOKEN:-}}"
fi
if [ -n "${repo_auth_fallback_token}" ]; then
  GITEA_PUSH_MIRROR_ENABLED="1"
  GITEA_PUSH_MIRROR_REPO_URL="${GITEA_PUSH_MIRROR_REPO_URL:-${GITEA_SEED_SOURCE_REPO_URL:-}}"
  GITEA_PUSH_MIRROR_USERNAME="${GITEA_PUSH_MIRROR_USERNAME:-${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME:-}}}"
  GITEA_PUSH_MIRROR_TOKEN="${repo_auth_fallback_token}"
else
  GITEA_PUSH_MIRROR_ENABLED="${GITEA_PUSH_MIRROR_ENABLED:-}"
  GITEA_PUSH_MIRROR_REPO_URL="${GITEA_PUSH_MIRROR_REPO_URL:-${GITEA_SEED_SOURCE_REPO_URL:-}}"
  GITEA_PUSH_MIRROR_USERNAME="${GITEA_PUSH_MIRROR_USERNAME:-${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME:-}}}"
  GITEA_PUSH_MIRROR_TOKEN="${GITEA_PUSH_MIRROR_TOKEN:-${GITEA_SEED_SOURCE_TOKEN:-${ARGOCD_GITHUB_TOKEN:-}}}"
fi
GITEA_SEED_TARGET_OWNER="${GITEA_SEED_TARGET_OWNER:-gitea-admin}"
GITEA_SEED_TARGET_REPO="${GITEA_SEED_TARGET_REPO:-cluster}"

infer_repo_owner_from_url() {
  python3 -c 'import sys,urllib.parse; value=sys.argv[1] if len(sys.argv) > 1 else ""; owner=""; 
try:
    parsed=urllib.parse.urlparse(value)
    path=(parsed.path or "").strip("/")
    parts=path.split("/") if path else []
    owner=parts[-2] if len(parts) >= 2 else ""
except Exception:
    owner=""
print(owner)' "${1:-}"
}

infer_github_login_from_token() {
  local token="${1:-}"
  if [ -z "${token}" ]; then
    return 1
  fi
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
        "User-Agent": "cluster-bootstrap-runtime-env",
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

github_token_looks_like_pat() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*) return 0 ;;
    *) return 1 ;;
  esac
}

if [ -n "${ARGOCD_GITHUB_TOKEN}" ] && { [ -z "${ARGOCD_GITHUB_USERNAME}" ] || [ "${ARGOCD_GITHUB_USERNAME}" = "oauth2" ]; }; then
  if github_token_looks_like_pat "${ARGOCD_GITHUB_TOKEN}"; then
    ARGOCD_GITHUB_USERNAME="$(infer_github_login_from_token "${ARGOCD_GITHUB_TOKEN}" || true)"
    [ -n "${ARGOCD_GITHUB_USERNAME}" ] || ARGOCD_GITHUB_USERNAME="$(infer_repo_owner_from_url "${ARGOCD_GITHUB_REPO_URL:-}")"
    [ -n "${ARGOCD_GITHUB_USERNAME}" ] || ARGOCD_GITHUB_USERNAME="github-user"
  else
    ARGOCD_GITHUB_USERNAME="x-access-token"
  fi
fi

if [ -n "${GITEA_SEED_SOURCE_TOKEN}" ] && { [ -z "${GITEA_SEED_SOURCE_USERNAME:-}" ] || [ "${GITEA_SEED_SOURCE_USERNAME:-}" = "oauth2" ]; }; then
  if github_token_looks_like_pat "${GITEA_SEED_SOURCE_TOKEN}"; then
    GITEA_SEED_SOURCE_USERNAME="$(infer_github_login_from_token "${GITEA_SEED_SOURCE_TOKEN}" || true)"
    [ -n "${GITEA_SEED_SOURCE_USERNAME}" ] || GITEA_SEED_SOURCE_USERNAME="$(infer_repo_owner_from_url "${GITEA_SEED_SOURCE_REPO_URL:-${ARGOCD_GITHUB_REPO_URL:-}}")"
    [ -n "${GITEA_SEED_SOURCE_USERNAME}" ] || GITEA_SEED_SOURCE_USERNAME="${ARGOCD_GITHUB_USERNAME:-github-user}"
  else
    GITEA_SEED_SOURCE_USERNAME="${ARGOCD_GITHUB_USERNAME:-x-access-token}"
  fi
fi

strip_token_param() {
  python3 -c 'import sys,urllib.parse; value=sys.argv[1] if len(sys.argv) > 1 else ""; parsed=urllib.parse.urlparse(value) if value else None; query=[(k,v) for (k,v) in urllib.parse.parse_qsl(parsed.query, keep_blank_values=True) if k.lower() != "token"] if parsed else []; print(urllib.parse.urlunparse((parsed.scheme, parsed.netloc, parsed.path, parsed.params, urllib.parse.urlencode(query), parsed.fragment)) if parsed else "")' "${1:-}"
}

bootstrap_bundle_url_runtime="${BOOTSTRAP_BUNDLE_URL:-}"
if [ -z "${bootstrap_bundle_url_runtime}" ] && [ -n "${KS_BASE_URL:-}" ]; then
  bootstrap_bundle_url_runtime="${KS_BASE_URL%/}/iso/firstboot-payload/ansible-runner-bundle.tar.gz"
fi
bootstrap_bundle_url_runtime="$(strip_token_param "${bootstrap_bundle_url_runtime}")"

bootstrap_bundle_sha256_runtime="${BOOTSTRAP_BUNDLE_SHA256:-}"
if [ -z "${bootstrap_bundle_sha256_runtime}" ] && [ -f "dist/ansible-runner-bundle.tar.gz" ]; then
  bootstrap_bundle_sha256_runtime="$(sha256sum dist/ansible-runner-bundle.tar.gz | awk '{print $1}')"
fi

mkdir -p "$(dirname "${out_path}")"

emit_env_line() {
  local key="$1"
  local value="${2-}"
  printf '%s=%q\n' "${key}" "${value}"
}

{
  printf '%s\n' '# Generated bootstrap runtime env. Root-only.'
  emit_env_line ADAETUM_CONFIG_CONTRACT "${ADAETUM_CONFIG_CONTRACT:-platform/v1alpha1}"
  emit_env_line BOOTSTRAP_BUNDLE_URL "${bootstrap_bundle_url_runtime:-}"
  emit_env_line BOOTSTRAP_BUNDLE_SHA256 "${bootstrap_bundle_sha256_runtime:-}"
  emit_env_line TAILSCALE_AUTHKEY "${TAILSCALE_AUTHKEY:-}"
  emit_env_line TAILSCALE_OAUTH_CLIENT_SECRET "${TAILSCALE_OAUTH_CLIENT_SECRET:-}"
  emit_env_line ANSIBLE_RUNNER_HTTP_TOKEN "${ANSIBLE_RUNNER_HTTP_TOKEN:-}"
  emit_env_line ANSIBLE_RUNNER_DEPLOY_KEY_B64 "${ANSIBLE_RUNNER_DEPLOY_KEY_B64:-}"
  emit_env_line ARGOCD_GITHUB_USERNAME "${ARGOCD_GITHUB_USERNAME:-}"
  emit_env_line ARGOCD_GITHUB_TOKEN "${ARGOCD_GITHUB_TOKEN:-}"
  emit_env_line CLOUDFLARE_ACCOUNT_ID "${CLOUDFLARE_ACCOUNT_ID:-}"
  emit_env_line CLOUDFLARE_API_TOKEN "${CLOUDFLARE_API_TOKEN:-}"
  emit_env_line CLOUDFLARE_ZONE_API_TOKEN "${CLOUDFLARE_ZONE_API_TOKEN:-}"
  emit_env_line GITHUB_APP_ID "${GITHUB_APP_ID:-}"
  emit_env_line GITHUB_APP_INSTALLATION_ID "${GITHUB_APP_INSTALLATION_ID:-}"
  emit_env_line GITHUB_APP_PRIVATE_KEY_B64 "${GITHUB_APP_PRIVATE_KEY_B64:-}"
  emit_env_line GITEA_CANONICAL_HOST "${GITEA_CANONICAL_HOST:-}"
  emit_env_line GITEA_CANONICAL_URL "${GITEA_CANONICAL_URL:-}"
  emit_env_line GITEA_PUSH_MIRROR_ENABLED "${GITEA_PUSH_MIRROR_ENABLED:-}"
  emit_env_line GITEA_PUSH_MIRROR_REPO_URL "${GITEA_PUSH_MIRROR_REPO_URL:-}"
  emit_env_line GITEA_PUSH_MIRROR_USERNAME "${GITEA_PUSH_MIRROR_USERNAME:-}"
  emit_env_line GITEA_PUSH_MIRROR_TOKEN "${GITEA_PUSH_MIRROR_TOKEN:-}"
  emit_env_line GITEA_SEED_SOURCE_USERNAME "${GITEA_SEED_SOURCE_USERNAME:-}"
  emit_env_line GITEA_SEED_SOURCE_TOKEN "${GITEA_SEED_SOURCE_TOKEN:-}"
  emit_env_line TAILSCALE_USER_API_TOKEN "${TAILSCALE_USER_API_TOKEN:-}"
  emit_env_line RANCHER_CLOUDFLARED_ORIGIN_URL "${RANCHER_CLOUDFLARED_ORIGIN_URL:-}"
  emit_env_line RANCHER_CLOUDFLARED_HTTP_HOST_HEADER "${RANCHER_CLOUDFLARED_HTTP_HOST_HEADER:-}"
  emit_env_line RANCHER_CLOUDFLARED_TUNNEL_TOKEN "${RANCHER_CLOUDFLARED_TUNNEL_TOKEN:-}"
  emit_env_line RANCHER_CLOUDFLARED_TUNNEL_ID "${RANCHER_CLOUDFLARED_TUNNEL_ID:-}"
  emit_env_line CLOUDFLARED_INGRESS_PUBLIC_HOSTS "${CLOUDFLARED_INGRESS_PUBLIC_HOSTS:-}"
  emit_env_line INGRESS_CLOUDFLARED_ORIGIN_URL "${INGRESS_CLOUDFLARED_ORIGIN_URL:-}"
  emit_env_line INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY "${INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY:-}"
  emit_env_line BOOTSTRAP_BACKUP_PASSPHRASE "${BOOTSTRAP_BACKUP_PASSPHRASE:-}"
  emit_env_line BOOTSTRAP_BACKUP_PASSPHRASE_B64 "${BOOTSTRAP_BACKUP_PASSPHRASE_B64:-}"
  emit_env_line RANCHER_ADMIN_PASSWORD "${RANCHER_ADMIN_PASSWORD:-}"
  emit_env_line BOOTSTRAP_DEBUG_PASSWORD "${BOOTSTRAP_DEBUG_PASSWORD:-}"
  emit_env_line BOOTSTRAP_DEBUG_PASSWORD_HASH "${BOOTSTRAP_DEBUG_PASSWORD_HASH:-}"
  emit_env_line BOOTSTRAP_DEBUG_SSH_KEY "${BOOTSTRAP_DEBUG_SSH_KEY:-}"
} >"${out_path}"

chmod 0600 "${out_path}"
printf '%s\n' "${out_path}"
