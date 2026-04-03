#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC1091
. "${script_dir}/diagnostics.sh"

ANSIBLE_INVENTORY="${ANSIBLE_INVENTORY:-ansible/node-inventory.yml}"
ANSIBLE_PLAYBOOK="${ANSIBLE_PLAYBOOK:-ansible/playbooks/argocd.yml}"
BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
OPENBAO_NAMESPACE="${OPENBAO_NAMESPACE:-openbao}"
OPENBAO_POD="${OPENBAO_POD:-openbao-0}"
INGRESS_INTERNAL_VIP="${INGRESS_INTERNAL_VIP:-${PLATFORM_ROUTING_INTERNAL_VIP:-}}"
INGRESS_EXTERNAL_VIP="${INGRESS_EXTERNAL_VIP:-${PLATFORM_ROUTING_EXTERNAL_VIP:-}}"
phase60_log_root="${BOOTSTRAP_PHASE_LOG_DIR:-/var/log/bootstrap}"
PHASE60_LOG_FILE="${PHASE60_LOG_FILE:-${phase60_log_root}/phase60.log}"
PHASE60_ARGOCD_DEBUG="${PHASE60_ARGOCD_DEBUG:-1}"
PHASE60_ARGOCD_DEBUG_LOG="${PHASE60_ARGOCD_DEBUG_LOG:-${PHASE60_LOG_FILE}}"
PHASE60_RECONCILE_ONLY="${PHASE60_RECONCILE_ONLY:-0}"
PHASE60_MODE="${PHASE60_MODE:-handoff}"
BOOTSTRAP_GITEA_GOLDEN_PATH_REQUIRED="${BOOTSTRAP_GITEA_GOLDEN_PATH_REQUIRED:-1}"
PHASE60_GITEA_ROLLOUT_TIMEOUT="${PHASE60_GITEA_ROLLOUT_TIMEOUT:-600s}"
PHASE60_GITEA_PROGRESS_DEADLINE_SECONDS="${PHASE60_GITEA_PROGRESS_DEADLINE_SECONDS:-1800}"
PHASE60_GITEA_ROLLOUT_ATTEMPTS="${PHASE60_GITEA_ROLLOUT_ATTEMPTS:-2}"
PHASE60_GITEA_ROLLOUT_RESTART_ON_FAIL="${PHASE60_GITEA_ROLLOUT_RESTART_ON_FAIL:-1}"
PHASE60_GITEA_DEBUG_LOG="${PHASE60_GITEA_DEBUG_LOG:-${PHASE60_LOG_FILE}}"
PHASE60_CLOUDFLARED_ROLLOUT_TIMEOUT="${PHASE60_CLOUDFLARED_ROLLOUT_TIMEOUT:-300s}"
PHASE60_CLOUDFLARED_ROLLOUT_ATTEMPTS="${PHASE60_CLOUDFLARED_ROLLOUT_ATTEMPTS:-2}"
PHASE60_CLOUDFLARED_ROLLOUT_RESTART_ON_FAIL="${PHASE60_CLOUDFLARED_ROLLOUT_RESTART_ON_FAIL:-1}"
PHASE60_CLOUDFLARED_DEBUG_LOG="${PHASE60_CLOUDFLARED_DEBUG_LOG:-${PHASE60_LOG_FILE}}"
PHASE60_RANCHER_ROLLOUT_TIMEOUT="${PHASE60_RANCHER_ROLLOUT_TIMEOUT:-600s}"
PHASE60_RANCHER_DEBUG_LOG="${PHASE60_RANCHER_DEBUG_LOG:-${PHASE60_LOG_FILE}}"
PHASE60_RANCHER_DESIRED_REPLICAS="${PHASE60_RANCHER_DESIRED_REPLICAS:-auto}"
PHASE60_RANCHER_PROGRESS_DEADLINE_SECONDS="${PHASE60_RANCHER_PROGRESS_DEADLINE_SECONDS:-1800}"
PHASE60_RANCHER_STARTUP_FAILURE_THRESHOLD="${PHASE60_RANCHER_STARTUP_FAILURE_THRESHOLD:-60}"
PHASE60_RANCHER_STARTUP_PERIOD_SECONDS="${PHASE60_RANCHER_STARTUP_PERIOD_SECONDS:-5}"
PHASE60_RANCHER_STARTUP_TIMEOUT_SECONDS="${PHASE60_RANCHER_STARTUP_TIMEOUT_SECONDS:-5}"
PHASE60_DOMAIN_CHECK_STRICT="${PHASE60_DOMAIN_CHECK_STRICT:-0}"
PHASE60_LOCAL_DOMAIN_CHECK_STRICT="${PHASE60_LOCAL_DOMAIN_CHECK_STRICT:-0}"
PHASE60_DOMAIN_CHECK_RETRIES="${PHASE60_DOMAIN_CHECK_RETRIES:-18}"
PHASE60_DOMAIN_CHECK_DELAY="${PHASE60_DOMAIN_CHECK_DELAY:-10}"
PHASE60_DOMAIN_HTTP_SUCCESS_REGEX="${PHASE60_DOMAIN_HTTP_SUCCESS_REGEX:-^(2[0-9][0-9]|3[0-9][0-9]|401|403)$}"
PHASE60_INGRESS_FRONTDOOR_WAIT_TIMEOUT="${PHASE60_INGRESS_FRONTDOOR_WAIT_TIMEOUT:-300}"
PHASE60_INGRESS_FRONTDOOR_WAIT_DELAY="${PHASE60_INGRESS_FRONTDOOR_WAIT_DELAY:-5}"
CLUSTER_DOMAIN="${CLUSTER_DOMAIN:-example.services}"
CLUSTER_LOCAL_DOMAIN="${CLUSTER_LOCAL_DOMAIN:-}"
KUBE_SERVICE_DOMAIN="${KUBE_SERVICE_DOMAIN:-}"
GITEA_CANONICAL_URL="${GITEA_CANONICAL_URL:-}"
GITEA_INTERNAL_URL="${GITEA_INTERNAL_URL:-}"
GITEA_INTERNAL_SERVICE_NAME="${GITEA_INTERNAL_SERVICE_NAME:-}"
GITEA_CHART_REPO="${GITEA_CHART_REPO:-https://dl.gitea.io/charts/}"

case "${PHASE60_MODE}" in
  handoff|realize|full)
    ;;
  *)
    echo "invalid PHASE60_MODE=${PHASE60_MODE}; expected handoff, realize, or full" >&2
    exit 1
    ;;
esac

PHASE60_WARNING_ONLY=0
if [[ "${PHASE60_MODE}" == "realize" ]]; then
  PHASE60_WARNING_ONLY=1
fi
BOOTSTRAP_DIAG_PHASE="phase60"
BOOTSTRAP_DIAG_LOG_PATH="${PHASE60_LOG_FILE:-${BUNDLE_BOOTSTRAP_LOG_FILE:-}}"
bootstrap_diag_init
phase60_start_ts="$(date +%s)"
phase60_last_error_cmd=""
phase60_last_error_line=""

if [[ -z "${BUNDLE_BOOTSTRAP_LOG_FILE:-}" ]]; then
  mkdir -p "$(dirname "${PHASE60_LOG_FILE}")"
  exec >>"${PHASE60_LOG_FILE}" 2>&1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
cd "${repo_root}"

if [[ -z "${ANSIBLE_CONFIG:-}" && -f "${repo_root}/ansible/ansible.cfg" ]]; then
  export ANSIBLE_CONFIG="${repo_root}/ansible/ansible.cfg"
fi
# Keep role discovery stable regardless of caller working directory.
export ANSIBLE_ROLES_PATH="${repo_root}/ansible/automation-roles:${repo_root}/ansible/playbooks/roles:/etc/ansible/roles:/usr/share/ansible/roles"

if [[ -z "${KUBECONFIG:-}" ]]; then
  if [[ -f /etc/rancher/rke2/rke2.yaml ]]; then
    export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
  fi
fi

kubectl_bin=""
if command -v kubectl >/dev/null 2>&1; then
  kubectl_bin="$(command -v kubectl)"
elif [[ -x /var/lib/rancher/rke2/bin/kubectl ]]; then
  kubectl_bin="/var/lib/rancher/rke2/bin/kubectl"
else
  echo "kubectl not found; Phase 60 requires kubectl access to the cluster." >&2
  exit 1
fi

read_secret_key_plain() {
  local namespace="$1"
  local secret_name="$2"
  local key="$3"
  local raw=""
  raw="$("${kubectl_bin}" -n "${namespace}" get secret "${secret_name}" -o "jsonpath={.data.${key}}" 2>/dev/null || true)"
  if [[ -z "${raw}" ]]; then
    return 0
  fi
  printf '%s' "${raw}" | base64 -d 2>/dev/null | tr -d '\r\n' || true
}

base64url() {
  openssl base64 -A | tr '+/' '-_' | tr -d '='
}

github_app_mint_token() {
  local key_file=""
  local now=""
  local iat=""
  local exp=""
  local header=""
  local payload=""
  local unsigned=""
  local signature=""
  local jwt=""
  local token_json=""

  [ -n "${GITHUB_APP_ID:-}" ] || return 1
  [ -n "${GITHUB_APP_INSTALLATION_ID:-}" ] || return 1
  [ -n "${GITHUB_APP_PRIVATE_KEY_B64:-}" ] || return 1

  key_file="$(mktemp)"
  if ! printf "%s" "${GITHUB_APP_PRIVATE_KEY_B64}" | base64 -d > "${key_file}" 2>/dev/null; then
    rm -f "${key_file}"
    return 1
  fi

  now="$(date +%s)"
  iat=$((now - 60))
  exp=$((now + 540))
  header='{"alg":"RS256","typ":"JWT"}'
  payload="{\"iat\":${iat},\"exp\":${exp},\"iss\":\"${GITHUB_APP_ID}\"}"
  unsigned="$(printf '%s' "${header}" | base64url).$(printf '%s' "${payload}" | base64url)"
  signature="$(printf '%s' "${unsigned}" | openssl dgst -sha256 -sign "${key_file}" | base64url)"
  rm -f "${key_file}"

  jwt="${unsigned}.${signature}"
  token_json="$(
    curl -fsSL -X POST "https://api.github.com/app/installations/${GITHUB_APP_INSTALLATION_ID}/access_tokens" \
      -H "Authorization: Bearer ${jwt}" \
      -H "Accept: application/vnd.github+json"
  )" || return 1

  printf '%s' "${token_json}" | python3 - <<'PY'
import json,sys
try:
  print(json.load(sys.stdin).get("token",""))
except Exception:
  print("")
PY
}

github_app_refresh_repo_auth() {
  local token=""

  if [[ -n "${GITEA_SEED_SOURCE_TOKEN:-}" ]] || [[ -n "${ARGOCD_GITHUB_TOKEN:-}" ]]; then
    return 0
  fi

  if [[ -z "${GITHUB_APP_ID:-}" || -z "${GITHUB_APP_INSTALLATION_ID:-}" || -z "${GITHUB_APP_PRIVATE_KEY_B64:-}" ]]; then
    return 0
  fi

  echo "[phase60] minting GitHub App installation token for source repo access"
  token="$(github_app_mint_token || true)"
  if [[ -z "${token}" ]]; then
    echo "[phase60] WARNING: GitHub App token mint failed; source repo auth will rely on explicit seed token envs" >&2
    return 0
  fi

  export ARGOCD_GITHUB_TOKEN="${token}"
  export ARGOCD_GITHUB_USERNAME="x-access-token"
  if [[ -z "${GITEA_SEED_SOURCE_USERNAME:-}" ]]; then
    export GITEA_SEED_SOURCE_USERNAME="${ARGOCD_GITHUB_USERNAME}"
  fi
}

github_token_looks_like_pat() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*) return 0 ;;
    *) return 1 ;;
  esac
}

infer_repo_owner_from_url() {
  python3 - <<'PY' "${1:-}"
import sys, urllib.parse
value=(sys.argv[1] if len(sys.argv) > 1 else "").strip()
owner=""
try:
  parsed=urllib.parse.urlparse(value)
  path=(parsed.path or "").strip("/")
  parts=path.split("/") if path else []
  if len(parts) >= 2:
    owner=parts[-2]
except Exception:
  owner=""
print((owner or "").strip())
PY
}

retry_cmd() {
  local attempts="${1:-5}"
  local sleep_seconds="${2:-5}"
  shift 2
  local i=1
  while (( i <= attempts )); do
    if "$@"; then
      return 0
    fi
    if (( i == attempts )); then
      return 1
    fi
    sleep "${sleep_seconds}"
    i=$((i + 1))
  done
  return 1
}

infer_github_login_from_token() {
  python3 - <<'PY' "${1:-}"
import json, sys, urllib.request
token = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
if not token:
    raise SystemExit(1)
req = urllib.request.Request(
    "https://api.github.com/user",
    headers={
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "User-Agent": "cluster-phase60",
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

token_debug_kind() {
  local token="${1:-}"
  if [[ -z "${token}" ]]; then
    printf 'missing'
    return 0
  fi
  if github_token_looks_like_pat "${token}"; then
    printf 'pat'
    return 0
  fi
  case "${token}" in
    ghs_*) printf 'installation-token' ;;
    github_app_*) printf 'app-token' ;;
    *) printf 'opaque-token' ;;
  esac
}

phase60_log_source_repo_auth() {
  local source_repo_url="${1:-}"
  local source_repo_username="${2:-}"
  local source_repo_token="${3:-}"
  local token_source="${4:-unknown}"
  local username_source="${5:-unknown}"
  local token_kind=""
  local token_present="no"
  local token_length="0"

  if [[ -n "${source_repo_token}" ]]; then
    token_present="yes"
    token_length="${#source_repo_token}"
  fi
  token_kind="$(token_debug_kind "${source_repo_token}")"

  echo "[phase60] source repo auth summary:"
  echo "[phase60]   url=${source_repo_url:-<empty>}"
  echo "[phase60]   username=${source_repo_username:-<empty>} (source=${username_source})"
  echo "[phase60]   token_present=${token_present} kind=${token_kind} length=${token_length} source=${token_source}"
}

rehydrate_ingress_vip_config() {
  local secret_ns="ingress"
  local secret_name="ingress-vip-config"
  local internal_vip=""
  local external_vip=""

  if ! "${kubectl_bin}" -n "${secret_ns}" get secret "${secret_name}" >/dev/null 2>&1; then
    return 0
  fi

  # Only hydrate when the operator hasn't explicitly provided overrides.
  if [[ -n "${INGRESS_INTERNAL_VIP:-}" || -n "${INGRESS_EXTERNAL_VIP:-}" ]]; then
    return 0
  fi

  internal_vip="$(read_secret_key_plain "${secret_ns}" "${secret_name}" ingress_internal_vip)"
  external_vip="$(read_secret_key_plain "${secret_ns}" "${secret_name}" ingress_external_vip)"

  if [[ -n "${internal_vip}" ]]; then
    INGRESS_INTERNAL_VIP="${internal_vip}"
  fi
  if [[ -n "${external_vip}" ]]; then
    INGRESS_EXTERNAL_VIP="${external_vip}"
  fi

  if [[ -n "${INGRESS_INTERNAL_VIP:-}" || -n "${INGRESS_EXTERNAL_VIP:-}" ]]; then
    echo "[phase60] hydrated ingress VIP config from ${secret_ns}/${secret_name}"
  fi
}

detect_kube_service_domain() {
  local corefile=""
  corefile="$("${kubectl_bin}" -n kube-system get configmap rke2-coredns-rke2-coredns -o jsonpath='{.data.Corefile}' 2>/dev/null || true)"
  if [[ -z "${corefile}" ]]; then
    corefile="$("${kubectl_bin}" -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}' 2>/dev/null || true)"
  fi
  if [[ -z "${corefile}" ]]; then
    printf 'cluster.local'
    return 0
  fi
  python3 - <<'PY' "${corefile}"
import re,sys
corefile=(sys.argv[1] if len(sys.argv) > 1 else "")
m=re.search(r'kubernetes\s+([^\s{]+)', corefile)
print((m.group(1).strip() if m else "cluster.local") or "cluster.local")
PY
}

echo "[phase60] repo: ${repo_root}"
echo "[phase60] checking cluster connectivity"
bootstrap_diag_record \
  "phase=phase60" \
  "step=${PHASE60_MODE}" \
  "component=phase60" \
  "operation=run-start" \
  "severity=info" \
  "summary=phase60 ${PHASE60_MODE} starting" \
  "log_path=${BOOTSTRAP_DIAG_LOG_PATH}" \
  "realization=$([[ "${PHASE60_MODE}" == "realize" ]] && echo true || echo false)"
"${kubectl_bin}" version --client >/dev/null
"${kubectl_bin}" get ns >/dev/null

rehydrate_ingress_vip_config

if [[ -z "${KUBE_SERVICE_DOMAIN}" ]]; then
  KUBE_SERVICE_DOMAIN="$(detect_kube_service_domain)"
fi
KUBE_SERVICE_DOMAIN="${KUBE_SERVICE_DOMAIN:-cluster.local}"
KUBE_SERVICE_SUFFIX="svc.${KUBE_SERVICE_DOMAIN}"
if [[ -z "${GITEA_INTERNAL_SERVICE_NAME}" ]]; then
  GITEA_INTERNAL_SERVICE_NAME="gitea-http"
fi
GITEA_INTERNAL_SERVICE_HOST="${GITEA_INTERNAL_SERVICE_NAME}.gitea.${KUBE_SERVICE_SUFFIX}:3000"
if [[ -z "${GITEA_INTERNAL_URL}" ]]; then
  GITEA_INTERNAL_URL="http://${GITEA_INTERNAL_SERVICE_HOST}/"
fi
echo "[phase60] kube service domain: ${KUBE_SERVICE_DOMAIN}"

timeout_to_seconds() {
  local raw="${1:-300s}"
  local value="${raw//[[:space:]]/}"
  if [[ "${value}" =~ ^([0-9]+)s$ ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
  elif [[ "${value}" =~ ^([0-9]+)m$ ]]; then
    printf '%s' "$(( ${BASH_REMATCH[1]} * 60 ))"
  elif [[ "${value}" =~ ^([0-9]+)h$ ]]; then
    printf '%s' "$(( ${BASH_REMATCH[1]} * 3600 ))"
  elif [[ "${value}" =~ ^[0-9]+$ ]]; then
    printf '%s' "${value}"
  else
    printf '%s' "300"
  fi
}

read_secret_file() {
  local path="$1"
  if [[ -f "${path}" ]]; then
    tr -d '\n' <"${path}"
  fi
}

read_k8s_secret_key() {
  local namespace="$1"
  local secret_name="$2"
  local key="$3"
  local raw=""
  raw="$("${kubectl_bin}" -n "${namespace}" get secret "${secret_name}" -o "jsonpath={.data.${key}}" 2>/dev/null || true)"
  if [[ -n "${raw}" ]]; then
    printf '%s' "${raw}" | base64 -d 2>/dev/null || true
  fi
}

write_secret_file() {
  local name="$1"
  local value="$2"
  if [[ -z "${BOOTSTRAP_SECRET_DIR:-}" ]]; then
    return 0
  fi
  mkdir -p "${BOOTSTRAP_SECRET_DIR}"
  chmod 0700 "${BOOTSTRAP_SECRET_DIR}" 2>/dev/null || true
  printf '%s\n' "${value}" > "${BOOTSTRAP_SECRET_DIR}/${name}"
  chmod 0600 "${BOOTSTRAP_SECRET_DIR}/${name}" 2>/dev/null || true
}

generate_random_secret_value() {
  local length="${1:-24}"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 "${length}" | tr -d '\r\n'
    return 0
  fi
  if command -v python3 >/dev/null 2>&1; then
    python3 - <<'PY' "${length}"
import secrets
import string
import sys
length = int(sys.argv[1]) if len(sys.argv) > 1 else 24
alphabet = string.ascii_letters + string.digits + "-_"
print("".join(secrets.choice(alphabet) for _ in range(length)), end="")
PY
    return 0
  fi
  return 1
}

ensure_authentik_secret() {
  local secret_key="${1:-}"
  local postgresql_password="${2:-}"
  local bootstrap_password="${3:-}"
  local bootstrap_token="${4:-}"
  if [[ -z "${secret_key}" || -z "${postgresql_password}" || -z "${bootstrap_password}" || -z "${bootstrap_token}" ]]; then
    echo "[phase60] Authentik bootstrap secret is incomplete; cannot create authentik-secrets" >&2
    return 1
  fi
  "${kubectl_bin}" create namespace authentik --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null
  "${kubectl_bin}" -n authentik create secret generic authentik-secrets \
    --from-literal=secret_key="${secret_key}" \
    --from-literal=postgresql_password="${postgresql_password}" \
    --from-literal=password="${postgresql_password}" \
    --from-literal=postgres-password="${postgresql_password}" \
    --from-literal=bootstrap_password="${bootstrap_password}" \
    --from-literal=bootstrap_token="${bootstrap_token}" \
    --dry-run=client -o yaml | "${kubectl_bin}" -n authentik apply -f - >/dev/null
}

ensure_headlamp_admin_token() {
  local sa_name="headlamp"
  local secret_name="headlamp-admin-token"
  local token=""
  local attempt=0

  "${kubectl_bin}" create namespace headlamp --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null
  for attempt in $(seq 1 90); do
    if "${kubectl_bin}" -n headlamp get serviceaccount "${sa_name}" >/dev/null 2>&1; then
      break
    fi
    sleep 2
  done
  if ! "${kubectl_bin}" -n headlamp get serviceaccount "${sa_name}" >/dev/null 2>&1; then
    echo "[phase60] headlamp service account not ready yet; skipping token export" >&2
    return 1
  fi

  cat <<EOF | "${kubectl_bin}" -n headlamp apply -f - >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: ${secret_name}
  namespace: headlamp
  annotations:
    kubernetes.io/service-account.name: ${sa_name}
type: kubernetes.io/service-account-token
EOF

  for attempt in $(seq 1 30); do
    token="$(read_k8s_secret_key headlamp "${secret_name}" token)"
    if [[ -n "${token}" ]]; then
      write_secret_file headlamp_admin_username "${sa_name}"
      write_secret_file headlamp_admin_token "${token}"
      printf '%s' "${token}"
      return 0
    fi
    sleep 2
  done

  echo "[phase60] failed waiting for Headlamp admin service-account token" >&2
  return 1
}

patch_openbao_widget_fields_nonempty() {
  local openbao_token="${1:-}"
  shift || true
  local pair=""
  local patch_args=()

  if [[ -z "${openbao_token}" || -z "${kubectl_bin}" ]]; then
    return 0
  fi

  for pair in "$@"; do
    [[ "${pair}" == *=* ]] || continue
    if [[ -n "${pair#*=}" ]]; then
      patch_args+=("${pair}")
    fi
  done

  if (( ${#patch_args[@]} == 0 )); then
    return 0
  fi

  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
    bao kv patch secret/bootstrap/platform "${patch_args[@]}" >/dev/null 2>&1 || true
}

ensure_homepage_widget_secret() {
  local grafana_admin_password=""
  local cloudflare_account_id=""
  local cloudflare_api_auth=""
  local cloudflare_tunnel_id=""
  local tailscale_api_auth=""
  local tailscale_device_id=""
  local existing_grafana_admin_password=""
  local existing_cloudflare_account_id=""
  local existing_cloudflare_api_auth=""
  local existing_cloudflare_tunnel_id=""
  local existing_tailscale_api_auth=""
  local existing_tailscale_device_id=""

  grafana_admin_password="${HOMEPAGE_GRAFANA_ADMIN_PASSWORD:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/grafana_admin_password")}"
  cloudflare_account_id="${CLOUDFLARE_ACCOUNT_ID:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/cloudflare_account_id")}"
  cloudflare_api_auth="${CLOUDFLARE_API_TOKEN:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/cloudflare_api_token")}"
  cloudflare_tunnel_id="${RANCHER_CLOUDFLARED_TUNNEL_ID:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/rancher_cloudflared_tunnel_id")}"
  tailscale_api_auth="${HOMEPAGE_TAILSCALE_API_AUTH:-${TAILSCALE_USER_API_TOKEN:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/homepage_tailscale_api_auth")}}"
  tailscale_device_id="${HOMEPAGE_TAILSCALE_DEVICE_ID:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/homepage_tailscale_device_id")}"
  if [[ -z "${tailscale_device_id}" ]] && command -v tailscale >/dev/null 2>&1; then
    tailscale_device_id="$(
      tailscale status --json 2>/dev/null | python3 - <<'PY'
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print("")
    raise SystemExit(0)
self_data = data.get("Self") or {}
print((self_data.get("ID") or "").strip())
PY
    )"
  fi
  if [[ -z "${cloudflare_account_id}" && -n "${openbao_token:-}" ]]; then
    cloudflare_account_id="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=cloudflare_account_id secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${cloudflare_api_auth}" && -n "${openbao_token:-}" ]]; then
    cloudflare_api_auth="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=cloudflare_api_token secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${cloudflare_tunnel_id}" && -n "${openbao_token:-}" ]]; then
    cloudflare_tunnel_id="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv get -field=rancher_cloudflared_tunnel_id secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${tailscale_api_auth}" && -n "${openbao_token:-}" ]]; then
    tailscale_api_auth="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=homepage_tailscale_api_auth secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${tailscale_device_id}" && -n "${openbao_token:-}" ]]; then
    tailscale_device_id="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv get -field=homepage_tailscale_device_id secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  existing_grafana_admin_password="$(read_k8s_secret_key homepage homepage-widget-secrets HOMEPAGE_GRAFANA_ADMIN_PASSWORD)"
  existing_cloudflare_account_id="$(read_k8s_secret_key homepage homepage-widget-secrets HOMEPAGE_CLOUDFLARE_ACCOUNT_ID)"
  existing_cloudflare_api_auth="$(read_k8s_secret_key homepage homepage-widget-secrets HOMEPAGE_CLOUDFLARE_API_AUTH)"
  existing_cloudflare_tunnel_id="$(read_k8s_secret_key homepage homepage-widget-secrets HOMEPAGE_CLOUDFLARE_TUNNEL_ID)"
  existing_tailscale_api_auth="$(read_k8s_secret_key homepage homepage-widget-secrets HOMEPAGE_TAILSCALE_API_AUTH)"
  existing_tailscale_device_id="$(read_k8s_secret_key homepage homepage-widget-secrets HOMEPAGE_TAILSCALE_DEVICE_ID)"

  if [[ -z "${grafana_admin_password}" ]]; then
    grafana_admin_password="${existing_grafana_admin_password}"
  fi
  if [[ -z "${cloudflare_account_id}" ]]; then
    cloudflare_account_id="${existing_cloudflare_account_id}"
  fi
  if [[ -z "${cloudflare_api_auth}" ]]; then
    cloudflare_api_auth="${existing_cloudflare_api_auth}"
  fi
  if [[ -z "${cloudflare_tunnel_id}" ]]; then
    cloudflare_tunnel_id="${existing_cloudflare_tunnel_id}"
  fi
  if [[ -z "${tailscale_api_auth}" ]]; then
    tailscale_api_auth="${existing_tailscale_api_auth}"
  fi
  if [[ -z "${tailscale_device_id}" ]]; then
    tailscale_device_id="${existing_tailscale_device_id}"
  fi

  if [[ -z "${cloudflare_account_id}" || -z "${cloudflare_api_auth}" || -z "${cloudflare_tunnel_id}" ]]; then
    cloudflare_account_id=""
    cloudflare_api_auth=""
    cloudflare_tunnel_id=""
  fi

  if [[ -z "${tailscale_api_auth}" || -z "${tailscale_device_id}" ]]; then
    tailscale_api_auth=""
    tailscale_device_id=""
  fi

  if [[ -n "${grafana_admin_password}" ]]; then
    write_secret_file grafana_admin_password "${grafana_admin_password}"
  fi
  if [[ -n "${cloudflare_account_id}" ]]; then
    write_secret_file cloudflare_account_id "${cloudflare_account_id}"
  fi
  if [[ -n "${cloudflare_api_auth}" ]]; then
    write_secret_file cloudflare_api_token "${cloudflare_api_auth}"
  fi
  if [[ -n "${cloudflare_tunnel_id}" ]]; then
    write_secret_file rancher_cloudflared_tunnel_id "${cloudflare_tunnel_id}"
  fi
  if [[ -n "${tailscale_api_auth}" ]]; then
    write_secret_file homepage_tailscale_api_auth "${tailscale_api_auth}"
  fi
  if [[ -n "${tailscale_device_id}" ]]; then
    write_secret_file homepage_tailscale_device_id "${tailscale_device_id}"
  fi

  if [[ -z "${grafana_admin_password}" && -z "${cloudflare_account_id}" && -z "${cloudflare_api_auth}" && -z "${cloudflare_tunnel_id}" && -z "${tailscale_api_auth}" && -z "${tailscale_device_id}" ]]; then
    echo "[phase60] homepage widget secrets unavailable; skipping homepage-widget-secrets"
    return 0
  fi

  "${kubectl_bin}" create namespace homepage --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null
  "${kubectl_bin}" -n homepage create secret generic homepage-widget-secrets \
    --from-literal=HOMEPAGE_GRAFANA_ADMIN_PASSWORD="${grafana_admin_password}" \
    --from-literal=HOMEPAGE_CLOUDFLARE_ACCOUNT_ID="${cloudflare_account_id}" \
    --from-literal=HOMEPAGE_CLOUDFLARE_TUNNEL_ID="${cloudflare_tunnel_id}" \
    --from-literal=HOMEPAGE_CLOUDFLARE_API_AUTH="${cloudflare_api_auth}" \
    --from-literal=HOMEPAGE_TAILSCALE_API_AUTH="${tailscale_api_auth}" \
    --from-literal=HOMEPAGE_TAILSCALE_DEVICE_ID="${tailscale_device_id}" \
    --dry-run=client -o yaml | "${kubectl_bin}" -n homepage apply -f - >/dev/null

  patch_openbao_widget_fields_nonempty "${openbao_token:-}" \
    "grafana_admin_password=${grafana_admin_password}" \
    "cloudflare_account_id=${cloudflare_account_id}" \
    "cloudflare_api_token=${cloudflare_api_auth}" \
    "rancher_cloudflared_tunnel_id=${cloudflare_tunnel_id}" \
    "homepage_tailscale_api_auth=${tailscale_api_auth}" \
    "homepage_tailscale_device_id=${tailscale_device_id}"
}

ansible_runner_image_effective() {
  local registry_host=""
  if [[ -n "${ANSIBLE_RUNNER_IMAGE:-}" ]]; then
    registry_host="$(ansible_runner_registry_host_effective)"
    python3 - <<'PY' "${ANSIBLE_RUNNER_IMAGE}" "${registry_host}"
import sys
image = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
registry_host = (sys.argv[2] if len(sys.argv) > 2 else "").strip()
if not image or not registry_host or "/" not in image:
    print(image, end="")
    raise SystemExit(0)
parts = image.split("/", 1)
print(f"{registry_host}/{parts[1]}", end="")
PY
    return 0
  fi
  registry_host="$(ansible_runner_registry_host_effective)"
  printf '%s/%s/%s:latest' \
    "${registry_host}" \
    "${GITEA_SEED_TARGET_OWNER:-gitea-admin}" \
    "${ANSIBLE_RUNNER_IMAGE_NAME:-ansible-runner}"
}

ansible_runner_registry_host_pull() {
  local registry_host="${ANSIBLE_RUNNER_REGISTRY_HOST:-registry.${CLUSTER_DOMAIN}}"
  printf '%s' "${registry_host}"
}

ansible_runner_registry_host_effective() {
  local registry_host=""
  registry_host="$(ansible_runner_registry_host_pull)"
  printf '%s' "${registry_host}"
}

ansible_runner_registry_host_push() {
  local registry_host=""
  registry_host="$(ansible_runner_registry_host_pull)"
  case "${registry_host}" in
    ""|*".svc"|*".svc."*|*".cluster.local"*|*":3000")
      printf '%s' "${registry_host}"
      return 0
      ;;
  esac

  if [[ -n "${GITEA_INTERNAL_SERVICE_HOST:-}" ]]; then
    printf '%s' "${GITEA_INTERNAL_SERVICE_HOST}"
    return 0
  fi

  if [[ -n "${GITEA_INTERNAL_SERVICE_NAME:-}" && -n "${KUBE_SERVICE_SUFFIX:-}" ]]; then
    printf '%s.gitea.%s:3000' "${GITEA_INTERNAL_SERVICE_NAME}" "${KUBE_SERVICE_SUFFIX}"
    return 0
  fi

  registry_host="$(gitea_service_cluster_ip)"
  if [[ -n "${registry_host}" ]]; then
    printf '%s:3000' "${registry_host}"
    return 0
  fi

  printf '%s' "${ANSIBLE_RUNNER_REGISTRY_HOST:-registry.${CLUSTER_DOMAIN}}"
}

registry_base_url_for_host() {
  local registry_host="${1:-}"
  case "${registry_host}" in
    *".svc"|*".svc."*|*".cluster.local"*|*":3000")
      printf 'http://%s' "${registry_host}"
      ;;
    *)
      printf 'https://%s' "${registry_host}"
      ;;
  esac
}

ansible_runner_registry_base_url() {
  local registry_host=""
  registry_host="$(ansible_runner_registry_host_effective)"
  registry_base_url_for_host "${registry_host}"
}

ansible_runner_image_push_effective() {
  local pull_image=""
  local push_host=""
  local remainder=""

  pull_image="$(ansible_runner_image_effective)"
  push_host="$(ansible_runner_registry_host_push)"
  remainder="${pull_image#*/}"
  printf '%s/%s' "${push_host}" "${remainder}"
}

argocd_gitea_repo_url() {
  local owner="${1:-${GITEA_SEED_TARGET_OWNER:-gitea-admin}}"
  local repo="${2:-${GITEA_SEED_TARGET_REPO:-cluster}}"
  printf '%s%s/%s.git' "${GITEA_INTERNAL_URL}" "${owner}" "${repo}"
}

repo_url_is_github() {
  python3 - <<'PY' "${1:-}"
import sys
from urllib.parse import urlparse

value = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
host = ""
if value:
    try:
        host = (urlparse(value).hostname or "").strip().lower()
    except Exception:
        host = ""
raise SystemExit(0 if host in {"github.com", "www.github.com"} else 1)
PY
}

find_ready_gitea_pod() {
  local pod=""
  local selector=""
  for selector in \
    'app.kubernetes.io/instance=gitea,app.kubernetes.io/name=gitea' \
    'app=gitea'
  do
    pod="$("${kubectl_bin}" -n gitea get pod -l "${selector}" --field-selector=status.phase=Running \
      -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.conditions[?(@.type=="Ready")]}{.status}{"\n"}{end}{end}' 2>/dev/null \
      | awk '$2 == "True" {print $1; exit}' || true)"
    if [[ -n "${pod}" ]]; then
      printf '%s' "${pod}"
      return 0
    fi
  done
  return 1
}

start_gitea_local_portforward() {
  local pod=""
  local pf_port=""
  local pf_log=""
  local pf_pid=""
  local base_url=""
  local attempts=0

  pod="$(find_ready_gitea_pod)" || return 1
  pf_port="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
  pf_log="$(mktemp /tmp/gitea-registry-portforward.XXXXXX.log)"
  "${kubectl_bin}" -n gitea port-forward "pod/${pod}" "${pf_port}:3000" >"${pf_log}" 2>&1 &
  pf_pid=$!
  base_url="http://127.0.0.1:${pf_port}"

  for attempts in $(seq 1 30); do
    if curl -fsS "${base_url}/api/v1/version" >/dev/null 2>&1; then
      printf '%s|%s|%s' "${pf_pid}" "${pf_log}" "${base_url}"
      return 0
    fi
    sleep 1
  done

  kill "${pf_pid}" >/dev/null 2>&1 || true
  wait "${pf_pid}" >/dev/null 2>&1 || true
  rm -f "${pf_log}" >/dev/null 2>&1 || true
  return 1
}

stop_local_portforward() {
  local pf_pid="${1:-}"
  local pf_log="${2:-}"
  if [[ -n "${pf_pid}" ]]; then
    kill "${pf_pid}" >/dev/null 2>&1 || true
    wait "${pf_pid}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${pf_log}" ]]; then
    rm -f "${pf_log}" >/dev/null 2>&1 || true
  fi
}

gitea_service_cluster_ip() {
  local ip=""
  ip="$("${kubectl_bin}" -n gitea get svc gitea-bootstrap-access -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true)"
  ip="$(printf '%s' "${ip}" | tr -d '\r\n[:space:]')"
  if [[ -n "${ip}" && "${ip}" != "None" && "${ip}" != "<none>" ]]; then
    printf '%s' "${ip}"
    return 0
  fi

  ip="$("${kubectl_bin}" -n gitea get svc "${GITEA_INTERNAL_SERVICE_NAME}" -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true)"
  ip="$(printf '%s' "${ip}" | tr -d '\r\n[:space:]')"
  if [[ -n "${ip}" && "${ip}" != "None" && "${ip}" != "<none>" ]]; then
    printf '%s' "${ip}"
    return 0
  fi

  ip="$("${kubectl_bin}" -n gitea get endpoints "${GITEA_INTERNAL_SERVICE_NAME}" -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null || true)"
  ip="$(printf '%s' "${ip}" | tr -d '\r\n[:space:]')"
  if [[ -n "${ip}" && "${ip}" != "None" && "${ip}" != "<none>" ]]; then
    printf '%s' "${ip}"
    return 0
  fi

  ip="$("${kubectl_bin}" -n gitea get pod -l 'app.kubernetes.io/instance=gitea,app.kubernetes.io/name=gitea' \
    -o jsonpath='{range .items[*]}{.status.podIP}{"\n"}{end}' 2>/dev/null | head -n1)"
  ip="$(printf '%s' "${ip}" | tr -d '\r\n[:space:]')"
  if [[ -n "${ip}" && "${ip}" != "None" && "${ip}" != "<none>" ]]; then
    printf '%s' "${ip}"
  fi
}

ensure_gitea_runtime_host_aliases() {
  local gitea_ip=""
  local hosts_path="/etc/hosts"
  local begin_marker="# BEGIN ansible-runner bootstrap hosts"
  local end_marker="# END ansible-runner bootstrap hosts"
  local canonical_host=""
  local local_host=""
  local registry_host=""
  local rendered=""
  local current=""
  local trimmed=""
  local tmp=""

  gitea_ip="$(gitea_service_cluster_ip)"
  if [[ -z "${gitea_ip}" ]]; then
    echo "[phase60] warning: unable to determine Gitea service IP for runtime host aliases" >&2
    return 0
  fi

  canonical_host="$(python3 - <<'PY' "${GITEA_CANONICAL_URL:-}"
import sys, urllib.parse
value = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
if not value:
    print("")
    raise SystemExit(0)
parsed = urllib.parse.urlparse(value)
print((parsed.hostname or "").strip())
PY
)"
  local_host="${gitea_local_host:-}"
  registry_host="$(ansible_runner_registry_host_pull)"

  rendered="$(python3 - <<'PY' "${begin_marker}" "${end_marker}" "${gitea_ip}" "${canonical_host}" "${local_host}" "${registry_host}"
import sys
begin_marker, end_marker, ip = sys.argv[1], sys.argv[2], sys.argv[3]
hosts = []
for raw in sys.argv[4:]:
    host = (raw or "").strip()
    if not host or ":" in host:
        continue
    if host not in hosts:
        hosts.append(host)
print(begin_marker)
for host in hosts:
    print(f"{ip} {host}")
print(end_marker)
PY
)"

  current="$(cat "${hosts_path}" 2>/dev/null || true)"
  trimmed="$(python3 - <<'PY' "${current}" "${begin_marker}" "${end_marker}"
import sys
text = sys.argv[1] if len(sys.argv) > 1 else ""
begin_marker = sys.argv[2]
end_marker = sys.argv[3]
lines = text.splitlines()
out = []
skip = False
for line in lines:
    if line.strip() == begin_marker:
        skip = True
        continue
    if skip and line.strip() == end_marker:
        skip = False
        continue
    if not skip:
        out.append(line)
print("\n".join(out).rstrip("\n"))
PY
)"

  tmp="$(mktemp "${hosts_path}.XXXXXX")"
  if [[ -n "${trimmed}" ]]; then
    printf '%s\n' "${trimmed}" > "${tmp}"
  fi
  printf '%s\n' "${rendered}" >> "${tmp}"
  chmod 0644 "${tmp}" 2>/dev/null || true
  mv "${tmp}" "${hosts_path}"
  echo "[phase60] ensured runtime host aliases for ${canonical_host:-<none>} ${local_host:-<none>} via ${gitea_ip}"
}

wait_for_kube_api_ready() {
  local attempts="${1:-60}"
  local delay="${2:-5}"
  local i=""
  for i in $(seq 1 "${attempts}"); do
    if "${kubectl_bin}" get ns >/dev/null 2>&1; then
      return 0
    fi
    sleep "${delay}"
  done
  return 1
}

ensure_ansible_runner_registry_runtime() {
  local runner_image=""
  local registry_host=""
  local mirror_endpoint=""
  local bootstrap_registry_ip=""
  local registries_path="/etc/rancher/rke2/registries.yaml"
  local managed_marker="# ansible-runner bootstrap registry"
  local desired=""
  local current=""
  local tmp=""

  runner_image="$(ansible_runner_image_effective)"
  mapfile -t image_parts < <(ansible_runner_image_parts "${runner_image}")
  registry_host="${image_parts[0]}"
  bootstrap_registry_ip="$(gitea_service_cluster_ip)"

  case "${registry_host}" in
    ""|*".svc"|*".svc."*|*".cluster.local"*)
      mirror_endpoint="http://${registry_host}"
      ;;
    *:3000)
      mirror_endpoint="http://${registry_host}"
      ;;
    *)
      if [[ -n "${bootstrap_registry_ip}" ]]; then
        mirror_endpoint="http://${bootstrap_registry_ip}:3000"
      else
        echo "[phase60] warning: unable to resolve bootstrap Gitea registry endpoint for ${registry_host}; leaving runtime registry config unchanged" >&2
        return 0
      fi
      ;;
  esac

  current=""
  if [[ -f "${registries_path}" ]]; then
    current="$(cat "${registries_path}" 2>/dev/null || true)"
    if [[ -n "${current}" && "${current}" != *"${managed_marker}"* && "${current}" != *"\"${registry_host}\""* ]]; then
      echo "[phase60] warning: existing ${registries_path} is unmanaged; not overwriting it automatically" >&2
      return 0
    fi
  fi

  desired="$(cat <<EOF
${managed_marker}
mirrors:
  "${registry_host}":
    endpoint:
      - "${mirror_endpoint}"
configs:
  "${registry_host}":
    tls:
      insecure_skip_verify: true
EOF
)"

  if [[ "${current}" == "${desired}" ]]; then
  echo "[phase60] ansible-runner registry runtime config already present for ${registry_host}"
  return 0
  fi

  mkdir -p "$(dirname "${registries_path}")"
  tmp="$(mktemp "${registries_path}.XXXXXX")"
  printf '%s\n' "${desired}" > "${tmp}"
  chmod 0600 "${tmp}" 2>/dev/null || true
  mv "${tmp}" "${registries_path}"
  echo "[phase60] wrote ${registries_path} for bootstrap registry ${registry_host} via ${mirror_endpoint}"

  if command -v systemctl >/dev/null 2>&1; then
    echo "[phase60] restarting rke2-server to pick up registry config"
    systemctl restart rke2-server
    if ! wait_for_kube_api_ready 60 5; then
      echo "[phase60] Kubernetes API did not return after restarting rke2-server" >&2
      return 1
    fi
    echo "[phase60] rke2-server restarted and Kubernetes API is reachable again"
  else
    echo "[phase60] warning: systemctl not found; restart rke2-server manually to apply ${registries_path}" >&2
  fi
}

detect_gitea_internal_service_name() {
  python3 - <<'PY' "${kubectl_bin}"
import json, subprocess, sys
kubectl = sys.argv[1]
try:
    out = subprocess.check_output([kubectl, "-n", "gitea", "get", "svc", "-o", "json"], text=True)
    data = json.loads(out)
except Exception:
    print("")
    raise SystemExit(0)

def score(item):
    md = item.get("metadata") or {}
    spec = item.get("spec") or {}
    name = (md.get("name") or "").strip()
    ports = spec.get("ports") or []
    selector = spec.get("selector") or {}
    port_3000 = any(str(p.get("port", "")) == "3000" or str(p.get("targetPort", "")) == "3000" for p in ports)
    if not port_3000:
        return (-1, name)
    s = 0
    if selector.get("app.kubernetes.io/name") == "gitea":
        s += 5
    if selector.get("app.kubernetes.io/instance") == "gitea":
        s += 3
    if selector.get("app") == "gitea":
        s += 2
    if "http" in name:
        s += 2
    if "gitea" in name:
        s += 1
    return (s, name)

items = data.get("items") or []
candidates = [item for item in items if score(item)[0] >= 0]
if not candidates:
    print("")
else:
    candidates.sort(key=lambda item: score(item), reverse=True)
    print(((candidates[0].get("metadata") or {}).get("name") or "").strip())
PY
}

ensure_gitea_bootstrap_access_service() {
  local selector_yaml=""

  selector_yaml="$(python3 - <<'PY' "${kubectl_bin}" "${GITEA_INTERNAL_SERVICE_NAME}"
import json, subprocess, sys
kubectl = sys.argv[1]
svc_name = sys.argv[2]
try:
    out = subprocess.check_output([kubectl, "-n", "gitea", "get", "svc", svc_name, "-o", "json"], text=True)
    data = json.loads(out)
except Exception:
    print("")
    raise SystemExit(0)
selector = ((data.get("spec") or {}).get("selector") or {})
if not selector:
    print("")
    raise SystemExit(0)
for key, value in selector.items():
    print(f"    {key}: {value}")
PY
)"

  if [[ -z "${selector_yaml}" ]]; then
    echo "[phase60] WARNING: unable to derive selector for bootstrap Gitea access service from ${GITEA_INTERNAL_SERVICE_NAME}" >&2
    return 1
  fi

  cat <<EOF | "${kubectl_bin}" -n gitea apply -f - >/dev/null
apiVersion: v1
kind: Service
metadata:
  name: gitea-bootstrap-access
  namespace: gitea
spec:
  type: ClusterIP
  selector:
${selector_yaml}
  ports:
    - name: http
      port: 80
      protocol: TCP
      targetPort: 3000
    - name: http-alt
      port: 3000
      protocol: TCP
      targetPort: 3000
EOF
  echo "[phase60] ensured bootstrap Gitea access service gitea-bootstrap-access"
}

refresh_gitea_internal_service_url() {
  local detected=""
  local service_host=""
  local bootstrap_host=""
  detected="$(detect_gitea_internal_service_name)"
  if [[ -n "${detected}" ]]; then
    GITEA_INTERNAL_SERVICE_NAME="${detected}"
    ensure_gitea_bootstrap_access_service >/dev/null 2>&1 || true
    service_host="${GITEA_INTERNAL_SERVICE_NAME}.gitea.${KUBE_SERVICE_SUFFIX}"
    bootstrap_host="$(gitea_service_cluster_ip)"
    GITEA_INTERNAL_SERVICE_HOST="${service_host}:3000"
    GITEA_INTERNAL_URL="http://${GITEA_INTERNAL_SERVICE_HOST}/"
    if [[ -n "${bootstrap_host}" ]]; then
      echo "[phase60] detected Gitea internal service -> ${GITEA_INTERNAL_SERVICE_NAME} (${GITEA_INTERNAL_SERVICE_HOST}); bootstrap registry endpoint ${bootstrap_host}:3000"
    else
      echo "[phase60] detected Gitea internal service -> ${GITEA_INTERNAL_SERVICE_NAME} (${GITEA_INTERNAL_SERVICE_HOST})"
    fi
  else
    echo "[phase60] WARNING: unable to detect Gitea internal HTTP service; keeping ${GITEA_INTERNAL_SERVICE_NAME}" >&2
  fi
}

ansible_runner_pull_secret_name() {
  printf '%s' "${ANSIBLE_RUNNER_IMAGE_PULL_SECRET:-gitea-registry-creds}"
}

ansible_runner_image_parts() {
  python3 - <<'PY' "${1:-}"
import sys
image = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
host, rest = image.split("/", 1)
repo, ref = rest.rsplit(":", 1)
print(host)
print(repo)
print(ref)
PY
}

gitea_registry_token_effective() {
  local token=""
  token="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_registry_token")"
  if [[ -n "${token}" ]]; then
    printf '%s' "${token}"
    return 0
  fi

  if [[ -n "${openbao_token:-}" ]]; then
    token="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=gitea_registry_token secret/bootstrap/platform 2>/dev/null || true
    )"
    if [[ -n "${token}" ]]; then
      write_secret_file gitea_registry_token "${token}"
      printf '%s' "${token}"
      return 0
    fi
  fi

  token="$("${kubectl_bin}" -n gitea exec deploy/gitea -c gitea -- sh -lc '
    gitea admin user generate-access-token \
      --username "'"${gitea_repo_username_effective}"'" \
      --token-name "ansible-runner-registry" \
      --scopes "all" \
      --raw 2>/dev/null \
    || gitea admin user generate-access-token \
      --username "'"${gitea_repo_username_effective}"'" \
      --token-name "ansible-runner-registry-$(date +%s)" \
      --scopes "all" \
      --raw 2>/dev/null
  ' 2>/dev/null || true)"
  if [[ -n "${token}" ]]; then
    write_secret_file gitea_registry_token "${token}"
    printf '%s' "${token}"
    return 0
  fi

  # The same personal access token that works for Git over HTTP also works for
  # the Gitea container registry in this bootstrap model. Reuse it instead of
  # making registry auth depend on a second token mint path.
  token="$(gitea_git_token_effective || true)"
  if [[ -n "${token}" ]]; then
    write_secret_file gitea_registry_token "${token}"
    printf '%s' "${token}"
  fi
}

gitea_git_token_effective() {
  local token=""
  token="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_git_token")"
  if [[ -n "${token}" ]]; then
    printf '%s' "${token}"
    return 0
  fi

  token="$("${kubectl_bin}" -n gitea exec deploy/gitea -c gitea -- sh -lc '
    gitea admin user generate-access-token \
      --username "'"${gitea_repo_username_effective}"'" \
      --token-name "argocd-bootstrap-git" \
      --scopes "all" \
      --raw 2>/dev/null \
    || gitea admin user generate-access-token \
      --username "'"${gitea_repo_username_effective}"'" \
      --token-name "argocd-bootstrap-git-$(date +%s)" \
      --scopes "all" \
      --raw 2>/dev/null
  ' 2>/dev/null || true)"
  if [[ -n "${token}" ]]; then
    write_secret_file gitea_git_token "${token}"
    printf '%s' "${token}"
  fi
}

ensure_ansible_runner_pull_secret() {
  local registry_host=""
  local push_host=""
  local registry_username=""
  local registry_token=""
  local secret_name=""
  local dockerconfigjson=""

  registry_host="$(ansible_runner_registry_host_effective)"
  push_host="$(ansible_runner_registry_host_push)"
  registry_username="${ANSIBLE_RUNNER_REGISTRY_USERNAME:-${gitea_repo_username_effective:-gitea-admin}}"
  registry_token="$(gitea_registry_token_effective)"
  secret_name="$(ansible_runner_pull_secret_name)"

  if [[ -z "${registry_token}" ]]; then
    echo "[phase60] failed to determine Gitea registry token for ansible-runner pulls" >&2
    return 1
  fi

  write_secret_file gitea_registry_host "${registry_host}"
  write_secret_file gitea_registry_username "${registry_username}"

  dockerconfigjson="$(
    python3 - <<'PY' "${registry_username}" "${registry_token}" "${registry_host}" "${push_host}"
import base64, json, sys
username = sys.argv[1]
token = sys.argv[2]
hosts = []
for host in sys.argv[3:]:
    host = host.strip()
    if host and host not in hosts:
        hosts.append(host)
auth = base64.b64encode(f"{username}:{token}".encode("utf-8")).decode("ascii")
payload = {
    "auths": {
        host: {
            "username": username,
            "password": token,
            "auth": auth,
        }
        for host in hosts
    }
}
print(json.dumps(payload, separators=(",", ":")), end="")
PY
  )"

  "${kubectl_bin}" create namespace ansible --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null 2>&1 || true
  cat <<EOF | "${kubectl_bin}" -n ansible apply -f - >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: ${secret_name}
  namespace: ansible
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: $(printf '%s' "${dockerconfigjson}" | base64 | tr -d '\r\n')
EOF

  if [[ "${push_host}" != "${registry_host}" ]]; then
    echo "[phase60] ensured ansible-runner imagePullSecret ${secret_name} for ${registry_host} and internal push host ${push_host}"
  else
    echo "[phase60] ensured ansible-runner imagePullSecret ${secret_name} for ${registry_host}"
  fi
}

container_registry_image_exists() {
  local runner_image="${1:-}"
  local registry_host=""
  local registry_base_url=""
  local repo_path=""
  local image_ref=""
  local registry_username=""
  local registry_token=""
  local bearer_token=""
  local status=""
  local pf_state=""
  local pf_pid=""
  local pf_log=""

  mapfile -t image_parts < <(ansible_runner_image_parts "${runner_image}")
  registry_host="${image_parts[0]}"
  repo_path="${image_parts[1]}"
  image_ref="${image_parts[2]}"
  registry_username="${ANSIBLE_RUNNER_REGISTRY_USERNAME:-${gitea_repo_username_effective:-gitea-admin}}"
  registry_token="$(gitea_registry_token_effective)"

  if [[ -z "${registry_token}" ]]; then
    return 1
  fi

  registry_base_url="$(registry_base_url_for_host "${registry_host}")"
  case "${registry_host}" in
    ${GITEA_INTERNAL_SERVICE_NAME}.gitea.svc|${GITEA_INTERNAL_SERVICE_NAME}.gitea.svc.*|*.cluster.local*|*".svc:"*)
      if pf_state="$(start_gitea_local_portforward 2>/dev/null)"; then
        IFS='|' read -r pf_pid pf_log registry_base_url <<<"${pf_state}"
      fi
      ;;
  esac

  bearer_token="$(
    curl -fsS -u "${registry_username}:${registry_token}" \
      "${registry_base_url}/v2/token?service=container_registry&scope=repository:${repo_path}:pull" \
      | python3 -c 'import sys, json; print(json.load(sys.stdin).get("token", ""))' 2>/dev/null || true
  )"
  if [[ -z "${bearer_token}" ]]; then
    stop_local_portforward "${pf_pid}" "${pf_log}"
    return 1
  fi

  status="$(
    curl -sS -o /dev/null -w '%{http_code}' \
      -H "Authorization: Bearer ${bearer_token}" \
      "${registry_base_url}/v2/${repo_path}/manifests/${image_ref}" || true
  )"
  stop_local_portforward "${pf_pid}" "${pf_log}"
  [[ "${status}" == "200" ]]
}

ansible_runner_image_exists() {
  container_registry_image_exists "$(ansible_runner_image_effective)"
}

publish_ansible_runner_image() {
  local runner_image=""
  local push_image=""
  local secret_name=""
  local job_name="ansible-runner-image-build"
  local manifest=""
  local timeout_secs="${PHASE60_ANSIBLE_RUNNER_BUILD_TIMEOUT_SECONDS:-900}"
  local wait_rc=0
  local elapsed=0
  local wait_step="${PHASE60_ANSIBLE_RUNNER_BUILD_STATUS_DELAY_SECONDS:-15}"
  local succeeded=""
  local failed=""
  local active=""
  local gitea_service_ip=""
  local host_alias_block=""
  local host_aliases=()

  runner_image="$(ansible_runner_image_effective)"
  push_image="$(ansible_runner_image_push_effective)"
  secret_name="$(ansible_runner_pull_secret_name)"

  if [[ "${push_image}" != "${runner_image}" ]] && container_registry_image_exists "${push_image}"; then
    echo "[phase60] ansible-runner image already present on internal registry push host: ${push_image}"
    return 0
  fi
  if [[ "${push_image}" == "${runner_image}" ]] && container_registry_image_exists "${runner_image}"; then
    echo "[phase60] ansible-runner image already present: ${runner_image}"
    return 0
  fi

  if [[ ! -f "${repo_root}/ansible/Dockerfile" ]]; then
    echo "[phase60] missing ${repo_root}/ansible/Dockerfile; cannot publish ansible-runner image" >&2
    return 1
  fi

  "${kubectl_bin}" -n ansible delete job "${job_name}" --ignore-not-found >/dev/null 2>&1 || true
  if [[ "${push_image}" != "${runner_image}" ]]; then
    echo "[phase60] kaniko will push ansible-runner to internal registry endpoint ${push_image} while the deployment continues to use ${runner_image}"
    gitea_service_ip="$(gitea_service_cluster_ip)"
    if [[ -n "${gitea_service_ip}" ]]; then
      if [[ -n "${CLUSTER_LOCAL_DOMAIN}" ]]; then
        host_aliases+=("gitea.${CLUSTER_LOCAL_DOMAIN}")
      fi
      if [[ -n "${CLUSTER_DOMAIN}" && "${CLUSTER_DOMAIN}" != "example.services" ]]; then
        host_aliases+=("gitea.${CLUSTER_DOMAIN}")
      fi
      host_alias_block="$(python3 - <<'PY' "${gitea_service_ip}" "${host_aliases[@]}"
import sys
ip = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
hosts = [h.strip() for h in sys.argv[2:] if h.strip()]
seen = []
for host in hosts:
    if host not in seen:
        seen.append(host)
if not ip or not seen:
    raise SystemExit(0)
print("      hostAliases:")
print(f"        - ip: {ip}")
print("          hostnames:")
for host in seen:
    print(f"            - {host}")
PY
)"
      if [[ -n "${host_alias_block}" ]]; then
        echo "[phase60] kaniko will resolve Gitea token-service hostnames via service IP ${gitea_service_ip}"
      fi
    fi
  fi

  manifest="$(cat <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${job_name}
  namespace: ansible
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: Never
${host_alias_block}
      imagePullSecrets:
        - name: ${secret_name}
      containers:
        - name: kaniko
          image: gcr.io/kaniko-project/executor:v1.23.2-debug
          args:
            - --context=dir:///workspace
            - --dockerfile=/workspace/ansible/Dockerfile
            - --destination=${push_image}
            - --snapshot-mode=redo
            - --use-new-run
          volumeMounts:
            - name: workspace
              mountPath: /workspace
            - name: docker-config
              mountPath: /kaniko/.docker
      volumes:
        - name: workspace
          hostPath:
            path: /opt/ansible-runner
            type: Directory
        - name: docker-config
          secret:
            secretName: ${secret_name}
            items:
              - key: .dockerconfigjson
                path: config.json
EOF
)"

  printf '%s\n' "${manifest}" | "${kubectl_bin}" apply -f - >/dev/null
  echo "[phase60] building ansible-runner image with kaniko job ${job_name}"
  echo "[phase60] waiting up to ${timeout_secs}s for kaniko build/push to ${push_image}"

  while (( elapsed < timeout_secs )); do
    succeeded="$("${kubectl_bin}" -n ansible get job "${job_name}" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)"
    failed="$("${kubectl_bin}" -n ansible get job "${job_name}" -o jsonpath='{.status.failed}' 2>/dev/null || true)"
    active="$("${kubectl_bin}" -n ansible get job "${job_name}" -o jsonpath='{.status.active}' 2>/dev/null || true)"

    if [[ "${succeeded}" =~ ^[1-9][0-9]*$ ]]; then
      wait_rc=0
      break
    fi
    if [[ "${failed}" =~ ^[1-9][0-9]*$ ]]; then
      wait_rc=1
      break
    fi
    if (( elapsed > 0 )) && (( elapsed % 60 == 0 )); then
      echo "[phase60] waiting for ${job_name}: elapsed=${elapsed}s active=${active:-0} succeeded=${succeeded:-0} failed=${failed:-0}"
    fi
    sleep "${wait_step}"
    elapsed=$(( elapsed + wait_step ))
  done

  if (( elapsed >= timeout_secs )) && [[ "${wait_rc}" == "0" ]]; then
    wait_rc=1
  fi

  if [[ "${wait_rc}" != "0" ]] && container_registry_image_exists "${push_image}"; then
    echo "[phase60] kaniko job no longer visible, but ansible-runner image is now present on the internal push host: ${push_image}"
    wait_rc=0
  fi

  if [[ "${wait_rc}" != "0" ]]; then
    echo "[phase60] ansible-runner image build job failed or timed out; logs follow" >&2
    "${kubectl_bin}" -n ansible get job "${job_name}" -o wide >&2 || true
    "${kubectl_bin}" -n ansible get pods -l "job-name=${job_name}" -o wide >&2 || true
    "${kubectl_bin}" -n ansible logs "job/${job_name}" --tail=200 >&2 || true
    return 1
  fi

  "${kubectl_bin}" -n ansible logs "job/${job_name}" --tail=50 || true

  if ! container_registry_image_exists "${push_image}"; then
    echo "[phase60] ansible-runner image still missing after build: ${push_image}" >&2
    return 1
  fi

  if [[ "${push_image}" != "${runner_image}" ]] && ! container_registry_image_exists "${runner_image}"; then
    echo "[phase60] published ansible-runner image to internal registry endpoint ${push_image}; canonical registry hostname ${runner_image} is not readable yet (expected during ingress/DNS convergence)"
    return 0
  fi

  echo "[phase60] published ansible-runner image ${runner_image}"
}

ensure_external_dns_cloudflare_secret() {
  local token=""
  local token_source=""

  if [[ -n "${CLOUDFLARE_ZONE_API_TOKEN:-}" ]]; then
    token="${CLOUDFLARE_ZONE_API_TOKEN}"
    token_source="CLOUDFLARE_ZONE_API_TOKEN"
  elif [[ -n "${CLOUDFLARE_API_TOKEN:-}" ]]; then
    token="${CLOUDFLARE_API_TOKEN}"
    token_source="CLOUDFLARE_API_TOKEN"
  fi

  if [[ -z "${token}" ]]; then
    return 1
  fi

  echo "[phase60] ensuring secret ingress/external-dns-cloudflare from ${token_source}"
  cat <<EOF | "${kubectl_bin}" -n ingress apply -f - >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: external-dns-cloudflare
  namespace: ingress
type: Opaque
stringData:
  api-token: ${token}
EOF

  write_secret_file external_dns_cloudflare_token_source "${token_source}"
  return 0
}

apply_ansible_runner_deployment() {
  local manifest_path="${repo_root}/pods/ansible/ansible/ansible-runner-deployment.yaml"
  local runner_image=""
  local rendered=""
  local pull_secret=""

  if [[ ! -f "${manifest_path}" ]]; then
    echo "[phase60] skip: missing ${manifest_path}"
    return 0
  fi

  runner_image="$(ansible_runner_image_effective)"
  pull_secret="$(ansible_runner_pull_secret_name)"
  write_secret_file ansible_runner_image "${runner_image}"

  rendered="$(python3 - <<'PY' "${manifest_path}" "${runner_image}" "${pull_secret}"
import pathlib, sys
path = pathlib.Path(sys.argv[1])
image = sys.argv[2]
pull_secret = sys.argv[3]
text = path.read_text(encoding="utf-8")
text = text.replace("gitea.example.local/gitea-admin/ansible-runner:latest", image)
text = text.replace("registry.example.local/gitea-admin/ansible-runner:latest", image)
text = text.replace("gitea-http.gitea.svc.cluster.local:3000/gitea-admin/ansible-runner:latest", image)
text = text.replace("name: gitea-registry-creds", f"name: {pull_secret}")
print(text, end="")
PY
)"

  "${kubectl_bin}" create namespace ansible --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null 2>&1 || true
  printf '%s' "${rendered}" | "${kubectl_bin}" apply -f -
  echo "[phase60] applied ansible-runner deployment with image ${runner_image}"
}

apply_external_dns_manifests() {
  local base_dir="${repo_root}/pods/ingress/external-dns"
  local deployment_path="${base_dir}/deployment.yaml"
  local rbac_path="${base_dir}/rbac.yaml"
  local rendered_deployment=""

  if [[ ! -f "${deployment_path}" || ! -f "${rbac_path}" ]]; then
    echo "[phase60] missing external-dns manifest inputs under ${base_dir}" >&2
    return 1
  fi

  write_secret_file external_dns_domain_filter "${CLUSTER_DOMAIN}"

  # Clean up the old bootstrap deployment that was incorrectly created in default.
  "${kubectl_bin}" -n default delete deployment external-dns --ignore-not-found >/dev/null 2>&1 || true
  "${kubectl_bin}" -n ingress apply -f "${rbac_path}" >/dev/null 2>&1
  rendered_deployment="$(python3 - <<'PY' "${deployment_path}" "${CLUSTER_DOMAIN}"
import pathlib, sys
path = pathlib.Path(sys.argv[1])
domain = sys.argv[2]
text = path.read_text(encoding="utf-8")
text = text.replace("--domain-filter=example.services", f"--domain-filter={domain}")
print(text, end="")
PY
)"
  printf '%s' "${rendered_deployment}" | "${kubectl_bin}" -n ingress apply -f -
  echo "[phase60] applied external-dns manifests with domain filter ${CLUSTER_DOMAIN}"
}

extract_ingress_lb_settings() {
  local svc_namespace="$1"
  local svc_name="$2"
  local svc_json=""
  svc_json="$("${kubectl_bin}" -n "${svc_namespace}" get svc "${svc_name}" -o json 2>/dev/null || true)"
  python3 - <<'PY' "${svc_json}"
import json,sys
try:
    svc=json.loads(sys.argv[1] if len(sys.argv) > 1 else "{}")
except Exception:
    print("|")
    raise SystemExit(0)
spec=svc.get("spec",{}) or {}
meta=svc.get("metadata",{}) or {}
ann=meta.get("annotations",{}) or {}
status=svc.get("status",{}) or {}
lb=status.get("loadBalancer",{}) or {}
ing=lb.get("ingress",[]) or []
vip=(spec.get("loadBalancerIP") or "").strip()
if vip in ("0.0.0.0", "::"):
    vip=""
if not vip:
    vip=(ann.get("kube-vip.io/loadbalancerIPs") or ann.get("kube-vip.io/loadbalancerIP") or "").strip()
if vip in ("0.0.0.0", "::"):
    vip=""
status_ip=""
if ing:
    first=ing[0] or {}
    status_ip=(first.get("ip") or first.get("hostname") or "").strip()
print(f"{vip}|{status_ip}")
PY
}

wait_for_concrete_ingress_vip() {
  local svc_namespace="$1"
  local svc_name="$2"
  local timeout="${3:-180}"
  local delay="${4:-5}"
  local elapsed=0
  local pair="" vip="" status_vip=""

  while [[ "${elapsed}" -le "${timeout}" ]]; do
    pair="$(extract_ingress_lb_settings "${svc_namespace}" "${svc_name}")"
    vip="${pair%%|*}"
    status_vip="${pair#*|}"
    if [[ -n "${vip}" ]]; then
      printf '%s' "${vip}"
      return 0
    fi
    if [[ -n "${status_vip}" ]]; then
      printf '%s' "${status_vip}"
      return 0
    fi
    sleep "${delay}"
    elapsed=$((elapsed + delay))
  done

  return 1
}

extract_ingress_lb_class() {
  local svc_namespace="$1"
  local svc_name="$2"
  "${kubectl_bin}" -n "${svc_namespace}" get svc "${svc_name}" -o jsonpath='{.spec.loadBalancerClass}' 2>/dev/null \
    | tr -d '\r\n' || true
}

detect_fallback_node_ip() {
  local ip=""
  if command -v ip >/dev/null 2>&1; then
    ip="$(ip route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
  fi
  if [[ -z "${ip}" ]] && command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  if [[ -z "${ip}" ]] && command -v ipconfig >/dev/null 2>&1; then
    ip="$(ipconfig getifaddr en0 2>/dev/null || true)"
    if [[ -z "${ip}" ]]; then
      ip="$(ipconfig getifaddr en1 2>/dev/null || true)"
    fi
  fi
  printf '%s' "${ip}"
}

apply_kube_vip_manifests() {
  local kube_vip_dir="${repo_root}/pods/ingress/kube-vip"
  local ingress_vip_dir="${repo_root}/pods/ingress/ingress-vip"

  if [[ ! -d "${kube_vip_dir}" ]]; then
    fail_local_requirement "missing ${kube_vip_dir}"
  fi
  if [[ ! -d "${ingress_vip_dir}" ]]; then
    fail_local_requirement "missing ${ingress_vip_dir}"
  fi

  echo "[phase60] applying pods/ingress/kube-vip"
  run_or_fail \
    "failed applying pods/ingress/kube-vip" \
    "${kubectl_bin}" -n kube-system apply -k "${kube_vip_dir}"

  echo "[phase60] waiting for kube-vip daemonset rollout"
  run_or_fail \
    "kube-vip daemonset did not become ready" \
    "${kubectl_bin}" -n kube-system rollout status daemonset/kube-vip-ds --timeout="${PHASE60_KUBE_VIP_ROLLOUT_TIMEOUT:-180s}"

  echo "[phase60] applying pods/ingress/ingress-vip"
  run_or_fail \
    "failed applying pods/ingress/ingress-vip" \
    "${kubectl_bin}" -n ingress apply -k "${ingress_vip_dir}"

  echo "[phase60] ensuring ingress-vip cronjob exists"
  run_or_fail \
    "ingress-vip cronjob missing after apply" \
    "${kubectl_bin}" -n ingress get cronjob ingress-vip
}

ensure_routing_frontdoor_vip() {
  local external_dns_hosts="${1:-}"
  local desired_vip=""
  local desired_service_type="LoadBalancer"
  local existing_lb_class=""
  local concrete_vip=""
  local effective_pair=""
  local vip_eff=""
  local status_vip_eff=""

  if ! "${kubectl_bin}" create namespace ingress --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null; then
    fail_local_requirement "failed to ensure ingress namespace exists"
  fi

  run_or_fail \
    "failed applying kube-vip manifests" \
    apply_kube_vip_manifests

  routing_service_ref="$(wait_for_routing_frontdoor_service || true)"
  if [[ -z "${routing_service_ref}" ]]; then
    fail_local_requirement "missing authoritative ingress front-door Service after wait (expected kube-system/rke2-ingress-nginx-controller or ingress-nginx/ingress-nginx-controller)"
  fi

  routing_service_ns="${routing_service_ref%%/*}"
  routing_service_name="${routing_service_ref##*/}"
  desired_vip="${INGRESS_EXTERNAL_VIP:-${INGRESS_INTERNAL_VIP:-}}"
  existing_lb_class="$(extract_ingress_lb_class "${routing_service_ns}" "${routing_service_name}")"

  echo "[phase60] setting ${routing_service_ref} type=${desired_service_type}"
  run_or_fail \
    "failed setting service type on ${routing_service_ref}" \
    "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
      -p "{\"spec\":{\"type\":\"${desired_service_type}\"}}"

  existing_lb_class="$(extract_ingress_lb_class "${routing_service_ns}" "${routing_service_name}")"
  if [[ -n "${existing_lb_class}" ]]; then
    echo "[phase60] warning: ${routing_service_ref} already has loadBalancerClass=${existing_lb_class}; Phase 50 will not mutate it" >&2
  fi

  if [[ -n "${desired_vip}" ]]; then
    echo "[phase60] pinning ${routing_service_ref} vip=${desired_vip}"
    run_or_fail \
      "failed pinning VIP on ${routing_service_ref}" \
      "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
        -p "{\"spec\":{\"loadBalancerIP\":\"${desired_vip}\"},\"metadata\":{\"annotations\":{\"kube-vip.io/loadbalancerIP\":null,\"kube-vip.io/loadbalancerIPs\":\"${desired_vip}\"}}}"
  else
    echo "[phase60] requesting ${routing_service_ref} vip via DHCP (0.0.0.0)"
    run_or_fail \
      "failed requesting DHCP VIP on ${routing_service_ref}" \
      "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
        -p "{\"spec\":{\"loadBalancerIP\":\"0.0.0.0\"},\"metadata\":{\"annotations\":{\"kube-vip.io/loadbalancerIP\":\"0.0.0.0\",\"kube-vip.io/loadbalancerIPs\":null}}}"

    concrete_vip="$(wait_for_concrete_ingress_vip "${routing_service_ns}" "${routing_service_name}" "${PHASE60_INGRESS_VIP_WAIT_TIMEOUT:-180}" "${PHASE60_INGRESS_VIP_WAIT_DELAY:-5}" || true)"
    if [[ -z "${concrete_vip}" ]]; then
      fail_local_requirement "failed waiting for concrete ingress VIP assignment on ${routing_service_ref}"
    fi
    echo "[phase60] ${routing_service_ref} assigned vip=${concrete_vip}"
    desired_vip="${concrete_vip}"
    run_or_fail \
      "failed persisting assigned VIP on ${routing_service_ref}" \
      "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
        -p "{\"spec\":{\"loadBalancerIP\":\"${desired_vip}\"},\"metadata\":{\"annotations\":{\"kube-vip.io/loadbalancerIP\":null,\"kube-vip.io/loadbalancerIPs\":\"${desired_vip}\"}}}"
  fi

  if [[ -n "${external_dns_hosts}" ]]; then
    "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
      -p "{\"metadata\":{\"annotations\":{\"external-dns.alpha.kubernetes.io/hostname\":\"${external_dns_hosts}\"}}}" >/dev/null 2>&1 || true
  fi

  effective_pair="$(extract_ingress_lb_settings "${routing_service_ns}" "${routing_service_name}")"
  vip_eff="${effective_pair%%|*}"
  status_vip_eff="${effective_pair#*|}"

  if [[ -z "${vip_eff}" && -n "${status_vip_eff}" ]]; then
    vip_eff="${status_vip_eff}"
  fi
  if [[ -z "${vip_eff}" ]]; then
    echo "[phase60] warning: unable to determine ingress VIP from ${routing_service_ref}; skipping strict VIP/DNS IP match for local domains"
    local_domain_expected_vip=""
  else
    internal_vip_eff="${vip_eff}"
    external_vip_eff="${vip_eff}"
    local_domain_expected_vip="${vip_eff}"
    emit_dns_update_window_notice \
      "${vip_eff}" \
      "$(gitea_local_domain_retry_count)" \
      "${PHASE60_DOMAIN_CHECK_DELAY}"
  fi
  if [[ -z "${internal_vip_eff:-}" ]]; then
    internal_vip_eff="${INGRESS_INTERNAL_VIP:-}"
  fi
  if [[ -z "${external_vip_eff:-}" ]]; then
    external_vip_eff="${INGRESS_EXTERNAL_VIP:-}"
  fi

  write_secret_file ingress_internal_vip "${internal_vip_eff}"
  write_secret_file ingress_external_vip "${external_vip_eff}"

  cat <<EOF | "${kubectl_bin}" -n ingress apply -f - >/dev/null 2>&1 || true
apiVersion: v1
kind: Secret
metadata:
  name: ingress-vip-config
type: Opaque
stringData:
  ingress_internal_vip: "${internal_vip_eff}"
  ingress_external_vip: "${external_vip_eff}"
  ingress_controller_service: "${routing_service_ref}"
EOF
}

reconcile_argocd_bootstrap_repo() {
  local repo_url="${1:-}"
  local repo_username="${2:-}"
  local repo_token="${3:-}"
  local repo_branch="${4:-HEAD}"
  local repo_path="${5:-pods/argocd}"
  local bootstrap_dir="${repo_root}/pods/argocd/bootstrap"
  local rendered_dir=""

  if [[ -z "${repo_url}" ]]; then
    echo "[phase60] Argo CD bootstrap repo URL is empty; refusing reconciliation" >&2
    return 1
  fi

  echo "[phase60] reconciling Argo CD bootstrap repo -> ${repo_url}"

  if [[ -n "${repo_token}" ]]; then
    cat <<EOF | "${kubectl_bin}" -n argocd apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: argocd-repo-https
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repo-creds
type: Opaque
stringData:
  url: ${repo_url}
  username: ${repo_username}
  password: ${repo_token}
  type: git
EOF

    cat <<EOF | "${kubectl_bin}" -n argocd apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: argocd-repository-bootstrap
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
type: Opaque
stringData:
  url: ${repo_url}
  username: ${repo_username}
  password: ${repo_token}
  type: git
  name: bootstrap-gitea
EOF
  fi

  "${kubectl_bin}" -n argocd delete application argocd --ignore-not-found >/dev/null 2>&1 || true

  if [[ ! -f "${bootstrap_dir}/app-of-apps.yaml" || ! -f "${bootstrap_dir}/applicationset.yaml" ]]; then
    echo "[phase60] bootstrap Argo CD manifests missing under ${bootstrap_dir}" >&2
    return 1
  fi

  rendered_dir="$(mktemp -d)"
  python3 - "${bootstrap_dir}" "${rendered_dir}" "${repo_url}" "${repo_branch}" <<'PY'
import pathlib
import re
import sys

src_dir = pathlib.Path(sys.argv[1])
dst_dir = pathlib.Path(sys.argv[2])
repo_url = sys.argv[3]
repo_branch = sys.argv[4] or "HEAD"

url_patterns = [
    r"http://[^/\s]+\.gitea\.svc\.cluster\.local:3000/gitea-admin/cluster\.git",
    r"http://[^/\s]+\.gitea\.svc\.[^/\s]+:3000/gitea-admin/cluster\.git",
    r"http://gitea\.[^/\s]+/gitea-admin/cluster\.git",
]

for name in ("app-of-apps.yaml", "applicationset.yaml"):
    text = (src_dir / name).read_text(encoding="utf-8")
    for pattern in url_patterns:
        text = re.sub(pattern, repo_url, text)
    if name == "app-of-apps.yaml":
        text = re.sub(r"(^\s{4}targetRevision:\s*).*$", r"\1" + repo_branch, text, flags=re.MULTILINE)
    elif name == "applicationset.yaml":
        text = re.sub(r"(^\s{8}revision:\s*).*$", r"\1" + repo_branch, text, count=1, flags=re.MULTILINE)
    (dst_dir / name).write_text(text, encoding="utf-8")
PY

  "${kubectl_bin}" -n argocd apply -f "${rendered_dir}/app-of-apps.yaml"
  "${kubectl_bin}" -n argocd apply -f "${rendered_dir}/applicationset.yaml"
  rm -rf "${rendered_dir}"
}

reconcile_pre_openbao_application_repo() {
  local repo_url="${1:-}"
  local repo_branch="${2:-HEAD}"
  local repo_path="${3:-pods/argocd/platform/pre-openbao}"

  if [[ -z "${repo_url}" ]]; then
    echo "[phase60] pre-openbao repo reconciliation skipped: repo URL missing" >&2
    return 1
  fi

  if ! "${kubectl_bin}" -n argocd get application platform-pre-openbao >/dev/null 2>&1; then
    echo "[phase60] pre-openbao application not present yet; skipping repo handoff"
    return 0
  fi

  cat <<EOF | "${kubectl_bin}" -n argocd apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-pre-openbao
  namespace: argocd
spec:
  source:
    repoURL: ${repo_url}
    targetRevision: ${repo_branch}
    path: ${repo_path}
EOF
}

verify_gitea_gitops_handoff() {
  local repo_url="${1:-}"
  local repo_username="${2:-}"
  local repo_token="${3:-}"
  local repo_branch="${4:-HEAD}"
  local pre_openbao_path="${5:-pods/argocd/platform/pre-openbao}"
  local app_repo_url=""
  local set_repo_url=""
  local secret_repo_url=""
  local pre_openbao_repo_url=""
  local pre_openbao_repo_path=""
  local askpass=""
  local refs_output=""

  if [[ -z "${repo_url}" ]]; then
    echo "[phase60] GitOps handoff verification failed: repo URL missing" >&2
    return 1
  fi

  app_repo_url="$("${kubectl_bin}" -n argocd get application app-of-apps -o jsonpath='{.spec.source.repoURL}' 2>/dev/null || true)"
  set_repo_url="$("${kubectl_bin}" -n argocd get applicationset apps -o jsonpath='{.spec.generators[0].git.repoURL}' 2>/dev/null || true)"
  secret_repo_url="$("${kubectl_bin}" -n argocd get secret argocd-repository-bootstrap -o jsonpath='{.stringData.url}' 2>/dev/null || true)"
  if [[ -z "${secret_repo_url}" ]]; then
    secret_repo_url="$("${kubectl_bin}" -n argocd get secret argocd-repository-bootstrap -o jsonpath='{.data.url}' 2>/dev/null | base64 -d 2>/dev/null || true)"
  fi
  pre_openbao_repo_url="$("${kubectl_bin}" -n argocd get application platform-pre-openbao -o jsonpath='{.spec.source.repoURL}' 2>/dev/null || true)"
  pre_openbao_repo_path="$("${kubectl_bin}" -n argocd get application platform-pre-openbao -o jsonpath='{.spec.source.path}' 2>/dev/null || true)"

  if [[ "${app_repo_url}" != "${repo_url}" ]]; then
    echo "[phase60] GitOps handoff verification failed: app-of-apps repoURL=${app_repo_url:-<empty>} expected=${repo_url}" >&2
    return 1
  fi
  if [[ "${set_repo_url}" != "${repo_url}" ]]; then
    echo "[phase60] GitOps handoff verification failed: applicationset repoURL=${set_repo_url:-<empty>} expected=${repo_url}" >&2
    return 1
  fi
  if [[ "${secret_repo_url}" != "${repo_url}" ]]; then
    echo "[phase60] GitOps handoff verification failed: argocd-repository-bootstrap url=${secret_repo_url:-<empty>} expected=${repo_url}" >&2
    return 1
  fi
  if [[ -n "${pre_openbao_repo_url}" && "${pre_openbao_repo_url}" != "${repo_url}" ]]; then
    echo "[phase60] GitOps handoff verification failed: platform-pre-openbao repoURL=${pre_openbao_repo_url} expected=${repo_url}" >&2
    return 1
  fi
  if [[ -n "${pre_openbao_repo_path}" && "${pre_openbao_repo_path}" != "${pre_openbao_path}" ]]; then
    echo "[phase60] GitOps handoff verification failed: platform-pre-openbao path=${pre_openbao_repo_path} expected=${pre_openbao_path}" >&2
    return 1
  fi

  if [[ -n "${repo_token}" && -n "${repo_username}" ]] && printf '%s' "${repo_url}" | grep -qE '^https?://'; then
    if ! gitea_repo_has_readable_refs_via_local_portforward "${repo_url}" "${repo_username}" "${repo_token}"; then
      echo "[phase60] GitOps handoff verification failed: gitea repo has no readable refs via local Gitea path for ${repo_url}" >&2
      return 1
    fi
    askpass="$(mktemp /tmp/gitea-gitops-askpass.XXXXXX)"
    cat >"${askpass}" <<'SH'
#!/bin/sh
case "$1" in
  *sername*) printf '%s\n' "${GIT_ASKPASS_USERNAME:-}" ;;
  *) printf '%s\n' "${GIT_ASKPASS_PASSWORD:-}" ;;
esac
SH
    chmod 0700 "${askpass}"
    refs_output="$(
      GIT_ASKPASS="${askpass}" \
      GIT_ASKPASS_USERNAME="${repo_username}" \
      GIT_ASKPASS_PASSWORD="${repo_token}" \
      GIT_TERMINAL_PROMPT=0 \
      git ls-remote "${repo_url}" "refs/heads/*" 2>/dev/null || true
    )"
    rm -f "${askpass}" >/dev/null 2>&1 || true
    if [[ -z "${refs_output}" ]]; then
      echo "[phase60] WARNING: gitea repo refs are readable via local Gitea path but not yet via canonical repo URL ${repo_url}; continuing (likely DNS/ingress convergence)" >&2
    fi
  fi

  echo "[phase60] verified GitOps handoff: Gitea repo is seeded and Argo CD points at ${repo_url} (${repo_branch})"
}

gitea_repo_has_readable_refs() {
  local repo_url="${1:-}"
  local repo_username="${2:-}"
  local repo_token="${3:-}"
  local askpass=""
  local refs_output=""

  if [[ -z "${repo_url}" || -z "${repo_username}" || -z "${repo_token}" ]]; then
    return 1
  fi

  askpass="$(mktemp /tmp/gitea-git-refs-askpass.XXXXXX)"
  cat >"${askpass}" <<'SH'
#!/bin/sh
case "$1" in
  *sername*) printf '%s\n' "${GIT_ASKPASS_USERNAME:-}" ;;
  *) printf '%s\n' "${GIT_ASKPASS_PASSWORD:-}" ;;
esac
SH
  chmod 0700 "${askpass}"
  refs_output="$(
    GIT_ASKPASS="${askpass}" \
    GIT_ASKPASS_USERNAME="${repo_username}" \
    GIT_ASKPASS_PASSWORD="${repo_token}" \
    GIT_TERMINAL_PROMPT=0 \
    git ls-remote "${repo_url}" "refs/heads/*" 2>/dev/null || true
  )"
  rm -f "${askpass}" >/dev/null 2>&1 || true

  [[ -n "${refs_output}" ]]
}

gitea_repo_has_readable_refs_via_local_portforward() {
  local repo_url="${1:-}"
  local repo_username="${2:-}"
  local repo_token="${3:-}"
  local pod=""
  local pf_port=""
  local pf_log=""
  local pf_pid=""
  local repo_path=""
  local local_repo_url=""

  if [[ -z "${repo_url}" || -z "${repo_username}" || -z "${repo_token}" ]]; then
    return 1
  fi

  for selector in \
    'app.kubernetes.io/instance=gitea,app.kubernetes.io/name=gitea' \
    'app=gitea'
  do
    pod="$("${kubectl_bin}" -n gitea get pod -l "${selector}" --field-selector=status.phase=Running \
      -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.conditions[?(@.type=="Ready")]}{.status}{"\n"}{end}{end}' 2>/dev/null \
      | awk '$2 == "True" {print $1; exit}' || true)"
    if [[ -n "${pod}" ]]; then
      break
    fi
  done
  if [[ -z "${pod}" ]]; then
    return 1
  fi

  repo_path="$(printf '%s' "${repo_url}" | sed -E 's#^https?://[^/]+/?##')"
  if [[ -z "${repo_path}" ]]; then
    return 1
  fi

  pf_port="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
  pf_log="$(mktemp /tmp/gitea-verify-portforward.XXXXXX.log)"
  "${kubectl_bin}" -n gitea port-forward "pod/${pod}" "${pf_port}:3000" >"${pf_log}" 2>&1 &
  pf_pid=$!
  sleep 2
  local_repo_url="http://127.0.0.1:${pf_port}/${repo_path}"

  if gitea_repo_has_readable_refs "${local_repo_url}" "${repo_username}" "${repo_token}"; then
    kill "${pf_pid}" >/dev/null 2>&1 || true
    wait "${pf_pid}" >/dev/null 2>&1 || true
    rm -f "${pf_log}" >/dev/null 2>&1 || true
    return 0
  fi

  kill "${pf_pid}" >/dev/null 2>&1 || true
  wait "${pf_pid}" >/dev/null 2>&1 || true
  rm -f "${pf_log}" >/dev/null 2>&1 || true
  return 1
}

ensure_gitea_admin_secret() {
  local admin_password="${1:-}"
  if [[ -z "${admin_password}" ]]; then
    echo "[phase60] Gitea admin password missing; cannot bootstrap Gitea via Argo" >&2
    return 1
  fi

  cat <<EOF | "${kubectl_bin}" -n gitea apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: gitea-admin-secret
  namespace: gitea
type: Opaque
stringData:
  username: gitea-admin
  password: ${admin_password}
  email: gitea-admin@example.com
EOF
}

configure_gitea_push_mirror() {
  local target_owner="${1:?owner}"
  local target_repo="${2:?repo}"
  local mirror_repo_url="${3:-}"
  local mirror_username="${4:-}"
  local mirror_token="${5:-}"
  local pod=""

  if [[ -z "${mirror_repo_url}" || -z "${mirror_username}" || -z "${mirror_token}" ]]; then
    echo "[phase60] Gitea push mirror is not fully configured; skipping" >&2
    return 0
  fi
  if ! repo_url_is_github "${mirror_repo_url}"; then
    echo "[phase60] Gitea push mirror target is not a GitHub repo; skipping auto push mirror for ${mirror_repo_url}" >&2
    return 0
  fi
  if ! github_token_looks_like_pat "${mirror_token}"; then
    echo "[phase60] Gitea push mirror requires a stable GitHub PAT-style token; skipping auto mirror setup" >&2
    return 0
  fi

  pod="$(find_ready_gitea_pod)" || {
    echo "[phase60] no ready Gitea pod found for push mirror setup" >&2
    return 1
  }

  if ! "${kubectl_bin}" -n gitea exec -i "${pod}" -c gitea -- env \
    TARGET_OWNER="${target_owner}" \
    TARGET_REPO="${target_repo}" \
    MIRROR_REPO_URL="${mirror_repo_url}" \
    MIRROR_USERNAME="${mirror_username}" \
    MIRROR_TOKEN="${mirror_token}" \
    sh -s -- <<'EOF'
set -eu

target_owner="${TARGET_OWNER:?}"
target_repo="${TARGET_REPO:?}"
mirror_repo_url="${MIRROR_REPO_URL:?}"
mirror_username="${MIRROR_USERNAME:?}"
mirror_token="${MIRROR_TOKEN:?}"

repo_path=""
for candidate in \
  "/data/git/repositories/${target_owner}/${target_repo}.git" \
  "/data/git/gitea-repositories/${target_owner}/${target_repo}.git"
do
  if [ -d "${candidate}" ]; then
    repo_path="${candidate}"
    break
  fi
done
if [ -z "${repo_path}" ]; then
  repo_path="$(find /data/git -type d -path "*/${target_owner}/${target_repo}.git" 2>/dev/null | head -n 1 || true)"
fi
if [ -z "${repo_path}" ] || [ ! -d "${repo_path}" ]; then
  echo "missing repo path ${repo_path}" >&2
  exit 1
fi

hooks_dir="${repo_path}/hooks"
mkdir -p "${hooks_dir}"

printf '%s' "${mirror_username}" >"${hooks_dir}/.github-mirror.username"
printf '%s' "${mirror_token}" >"${hooks_dir}/.github-mirror.password"
printf '%s' "${mirror_repo_url}" >"${hooks_dir}/.github-mirror.url"
chmod 0600 "${hooks_dir}/.github-mirror.username" "${hooks_dir}/.github-mirror.password" "${hooks_dir}/.github-mirror.url"

cat >"${hooks_dir}/.github-mirror-askpass.sh" <<'EOF_ASKPASS'
#!/bin/sh
hooks_dir="$(dirname "$0")"
case "$1" in
  *sername*) cat "${hooks_dir}/.github-mirror.username" ;;
  *) cat "${hooks_dir}/.github-mirror.password" ;;
esac
EOF_ASKPASS
chmod 0700 "${hooks_dir}/.github-mirror-askpass.sh"

cat >"${hooks_dir}/post-receive" <<'EOF_HOOK'
#!/bin/sh
set -eu
(
  hooks_dir="$(dirname "$0")"
  repo_dir="$(dirname "${hooks_dir}")"
  export GIT_TERMINAL_PROMPT=0
  export GIT_ASKPASS="${hooks_dir}/.github-mirror-askpass.sh"
  git -C "${repo_dir}" push --mirror "$(cat "${hooks_dir}/.github-mirror.url")" >>"${hooks_dir}/github-mirror.log" 2>&1 || true
) &
EOF_HOOK
chmod 0700 "${hooks_dir}/post-receive"

mirror_url="$(cat "${hooks_dir}/.github-mirror.url")"
export GIT_TERMINAL_PROMPT=0
export GIT_ASKPASS="${hooks_dir}/.github-mirror-askpass.sh"
if ! git -C "${repo_path}" push --mirror "${mirror_url}" >>"${hooks_dir}/github-mirror.log" 2>&1; then
  echo "initial mirror push failed for ${mirror_url}" >&2
  tail -n 20 "${hooks_dir}/github-mirror.log" >&2 || true
  exit 1
fi
EOF
  then
    echo "[phase60] failed to configure Gitea push mirror" >&2
    return 1
  fi

  echo "[phase60] configured Gitea push mirror to ${mirror_repo_url}"
}

ensure_gitea_actions_runner() {
  local runner_token="${1:-}"
  local instance_url="${2:-${GITEA_INTERNAL_URL:-}}"
  local runner_name="${3:-bootstrap-runner}"
  local runner_image="${GITEA_RUNNER_IMAGE:-gitea/act_runner:latest}"
  local dind_image="${GITEA_RUNNER_DIND_IMAGE:-docker:27-dind}"
  local runner_labels="${GITEA_RUNNER_LABELS:-ubuntu-latest:docker://gitea/runner-images:ubuntu-latest,ubuntu-24.04:docker://gitea/runner-images:ubuntu-24.04,ubuntu-22.04:docker://gitea/runner-images:ubuntu-22.04}"

  if [[ -z "${runner_token}" ]]; then
    echo "[phase60] Gitea runner token missing; cannot deploy actions runner" >&2
    return 1
  fi
  if [[ -z "${instance_url}" ]]; then
    echo "[phase60] Gitea instance URL missing; cannot deploy actions runner" >&2
    return 1
  fi

  "${kubectl_bin}" create namespace gitea --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null

  cat <<EOF | "${kubectl_bin}" -n gitea apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: gitea-actions-runner
  namespace: gitea
type: Opaque
stringData:
  token: ${runner_token}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gitea-actions-runner
  namespace: gitea
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gitea-actions-runner
  template:
    metadata:
      labels:
        app: gitea-actions-runner
    spec:
      restartPolicy: Always
      volumes:
        - name: runner-data
          emptyDir: {}
        - name: docker-run
          emptyDir: {}
        - name: docker-lib
          emptyDir: {}
      containers:
        - name: docker
          image: ${dind_image}
          securityContext:
            privileged: true
          env:
            - name: DOCKER_TLS_CERTDIR
              value: ""
          volumeMounts:
            - name: docker-run
              mountPath: /var/run
            - name: docker-lib
              mountPath: /var/lib/docker
        - name: runner
          image: ${runner_image}
          env:
            - name: DOCKER_HOST
              value: unix:///var/run/docker.sock
            - name: GITEA_INSTANCE_URL
              value: ${instance_url}
            - name: GITEA_RUNNER_NAME
              value: ${runner_name}
            - name: GITEA_RUNNER_LABELS
              value: ${runner_labels}
            - name: GITEA_RUNNER_REGISTRATION_TOKEN
              valueFrom:
                secretKeyRef:
                  name: gitea-actions-runner
                  key: token
          volumeMounts:
            - name: runner-data
              mountPath: /data
            - name: docker-run
              mountPath: /var/run
EOF

  "${kubectl_bin}" -n gitea rollout status deployment/gitea-actions-runner --timeout=300s >/dev/null
  echo "[phase60] ensured Gitea actions runner deployment with labels ${runner_labels}"
}

apply_bootstrap_gitea_application() {
  "${kubectl_bin}" create namespace gitea --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null
  cat <<EOF | "${kubectl_bin}" -n argocd apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: gitea
  namespace: argocd
spec:
  project: default
  source:
    repoURL: ${GITEA_CHART_REPO}
    chart: gitea
    targetRevision: "12.5.0"
    helm:
      values: |
        podSecurityContext:
          fsGroup: 1000
          fsGroupChangePolicy: OnRootMismatch

        strategy:
          type: Recreate

        persistence:
          enabled: true
          accessModes:
            - ReadWriteOnce
          size: 10Gi
          storageClass: longhorn
          annotations:
            helm.sh/resource-policy: keep

        postgresql-ha:
          enabled: false

        postgresql:
          enabled: true
          persistence:
            enabled: true
            size: 10Gi
            storageClass: longhorn
            annotations:
              helm.sh/resource-policy: keep

        ingress:
          enabled: false

        gitea:
          config:
            cache:
              ADAPTER: memory
            session:
              PROVIDER: memory
            queue:
              TYPE: level
            server:
              DOMAIN: $(python3 - "${GITEA_CANONICAL_URL}" <<'PY'
import sys
from urllib.parse import urlparse
url = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
host = urlparse(url).hostname if url else ""
print(host or "gitea.local")
PY
)
              ROOT_URL: ${GITEA_CANONICAL_URL}
          admin:
            existingSecret: gitea-admin-secret
            passwordMode: initialOnlyRequireReset

        valkey-cluster:
          enabled: false

        valkey:
          enabled: false
  destination:
    server: https://kubernetes.default.svc
    namespace: gitea
  ignoreDifferences:
    - group: ""
      kind: PersistentVolumeClaim
      name: gitea-shared-storage
      namespace: gitea
      jsonPointers:
        - /spec/volumeName
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
      - RespectIgnoreDifferences=true
EOF
}

wait_for_gitea_application() {
  local retries="${1:-60}"
  local delay="${2:-5}"
  local attempt=1

  while (( attempt <= retries )); do
    if "${kubectl_bin}" -n argocd get application gitea >/dev/null 2>&1 && \
       "${kubectl_bin}" -n gitea get deployment gitea >/dev/null 2>&1; then
      return 0
    fi
    sleep "${delay}"
    attempt=$((attempt + 1))
  done

  return 1
}

seed_gitea_bootstrap_repo() {
  local admin_password="${1:-}"
  local canonical_url="${2:-}"
  local source_repo_url="${3:-}"
  local source_repo_branch="${4:-}"
  local source_repo_token="${5:-}"
  local target_owner="${6:-gitea-admin}"
  local target_repo="${7:-cluster}"

  if [[ -z "${admin_password}" || -z "${canonical_url}" ]]; then
    echo "[phase60] missing inputs for Gitea seed helper" >&2
    return 1
  fi

  local pod=""
  local pf_port=""
  local pf_log=""
  local pf_pid=""
  local base_url=""
  local admin_token=""
  local tmp_dir=""
  local clone_url=""
  local detected_branch=""
  local askpass=""
  local source_askpass=""
  local source_repo_username=""
  local gitea_git_url=""
  local argocd_git_url=""
  local gitea_git_token=""
  local gitea_repo_username_for_git=""
  local seeded_from_remote="0"
  local cloned_remote_repo="0"
  local repo_exists="0"
  local repo_has_refs="0"
  local repo_meta=""
  local skip_remote_when_present="${GITEA_SEED_SKIP_REMOTE_IF_PRESENT:-1}"
  local trust_gitea_repo="${GITEA_SEED_TRUST_EXISTING_GITEA:-0}"
  local render_branch=""
  local source_repo_token_source="none"
  local source_repo_username_source="none"
  local github_app_token=""

  github_app_refresh_repo_auth || true
  if [[ -z "${source_repo_token}" ]]; then
    if [[ -n "${GITEA_SEED_SOURCE_TOKEN:-}" ]]; then
      source_repo_token="${GITEA_SEED_SOURCE_TOKEN}"
      source_repo_token_source="GITEA_SEED_SOURCE_TOKEN"
    elif [[ -n "${ARGOCD_GITHUB_TOKEN:-}" ]]; then
      source_repo_token="${ARGOCD_GITHUB_TOKEN}"
      source_repo_token_source="ARGOCD_GITHUB_TOKEN"
    fi
  fi
  if [[ -n "${GITEA_SEED_SOURCE_USERNAME:-}" ]]; then
    source_repo_username="${GITEA_SEED_SOURCE_USERNAME}"
    source_repo_username_source="GITEA_SEED_SOURCE_USERNAME"
  elif [[ -n "${ARGOCD_GITHUB_USERNAME:-}" ]]; then
    source_repo_username="${ARGOCD_GITHUB_USERNAME}"
    source_repo_username_source="ARGOCD_GITHUB_USERNAME"
  fi

  if [[ -n "${source_repo_url}" ]] && printf '%s' "${source_repo_url}" | grep -qE '^https://github.com/'; then
    if [[ -n "${GITHUB_APP_ID:-}" && -n "${GITHUB_APP_INSTALLATION_ID:-}" && -n "${GITHUB_APP_PRIVATE_KEY_B64:-}" ]]; then
      if [[ -z "${source_repo_token}" ]]; then
        github_app_token="$(github_app_mint_token || true)"
      elif ! github_token_looks_like_pat "${source_repo_token}"; then
        github_app_token="$(github_app_mint_token || true)"
      fi
      if [[ -n "${github_app_token}" ]]; then
        source_repo_token="${github_app_token}"
        source_repo_token_source="github-app-installation-token"
        source_repo_username="x-access-token"
        source_repo_username_source="github-app-installation-token"
      fi
    fi
  fi

  cleanup_seed() {
    if [[ -n "${pf_pid:-}" ]]; then
      kill "${pf_pid}" >/dev/null 2>&1 || true
      wait "${pf_pid}" >/dev/null 2>&1 || true
    fi
    if [[ -n "${pf_log:-}" ]]; then
      rm -f "${pf_log}" >/dev/null 2>&1 || true
    fi
    if [[ -n "${tmp_dir:-}" ]]; then
      rm -rf "${tmp_dir}" >/dev/null 2>&1 || true
    fi
  }
  trap cleanup_seed RETURN

  for selector in \
    'app.kubernetes.io/instance=gitea,app.kubernetes.io/name=gitea' \
    'app=gitea'
  do
    pod="$("${kubectl_bin}" -n gitea get pod -l "${selector}" --field-selector=status.phase=Running \
      -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.conditions[?(@.type=="Ready")]}{.status}{"\n"}{end}{end}' 2>/dev/null \
      | awk '$2 == "True" {print $1; exit}' || true)"
    if [[ -n "${pod}" ]]; then
      break
    fi
  done
  if [[ -z "${pod}" ]]; then
    echo "[phase60] no ready Gitea pod found for repo seed" >&2
    return 1
  fi

  pf_port="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
  pf_log="$(mktemp /tmp/gitea-seed-portforward.XXXXXX.log)"
  "${kubectl_bin}" -n gitea port-forward "pod/${pod}" "${pf_port}:3000" >"${pf_log}" 2>&1 &
  pf_pid=$!
  base_url="http://127.0.0.1:${pf_port}"
  for _ in $(seq 1 60); do
    if curl -fsS "${base_url}/api/v1/version" >/dev/null 2>&1; then
      break
    fi
    sleep 2
  done
  curl -fsS "${base_url}/api/v1/version" >/dev/null

  "${kubectl_bin}" -n gitea exec "${pod}" -c gitea -- gitea admin user change-password \
    --username gitea-admin \
    --password "${admin_password}" >/dev/null 2>&1 || true
  "${kubectl_bin}" -n gitea exec "${pod}" -c gitea -- gitea admin user must-change-password --unset gitea-admin \
    >/dev/null 2>&1 || true

  admin_token="$("${kubectl_bin}" -n gitea exec "${pod}" -c gitea -- sh -lc '
    gitea admin user generate-access-token --username "gitea-admin" --token-name "argocd" --scopes "all" --raw 2>/dev/null \
      | awk "NF{line=\$0} END{print line}"
  ' 2>/dev/null | tr -d '\r\n' || true)"
  if [[ -z "${admin_token}" ]]; then
    admin_token="$("${kubectl_bin}" -n gitea exec "${pod}" -c gitea -- sh -lc '
      gitea admin user generate-access-token --username "gitea-admin" --token-name "argocd-$(date +%s)" --scopes "all" --raw 2>/dev/null \
        | awk "NF{line=\$0} END{print line}"
    ' 2>/dev/null | tr -d '\r\n' || true)"
  fi
  if [[ -z "${admin_token}" ]]; then
    echo "[phase60] failed to mint bootstrap Gitea API token" >&2
    return 1
  fi

  if curl -fsS -H "Authorization: token ${admin_token}" "${base_url}/api/v1/repos/${target_owner}/${target_repo}" >/dev/null 2>&1; then
    repo_exists="1"
  else
    curl -fsS -H "Authorization: token ${admin_token}" -X POST "${base_url}/api/v1/user/repos" \
      -H "Content-Type: application/json" \
      -d "{\"name\":\"${target_repo}\",\"private\":true}" >/dev/null
  fi

  tmp_dir="$(mktemp -d)"
  gitea_git_url="${canonical_url}${target_owner}/${target_repo}.git"
  argocd_git_url="$(argocd_gitea_repo_url "${target_owner}" "${target_repo}")"
  clone_url=""
  if [[ "${repo_exists}" == "1" ]]; then
    repo_meta="$(curl -fsS -H "Authorization: token ${admin_token}" "${base_url}/api/v1/repos/${target_owner}/${target_repo}" 2>/dev/null || true)"
    if [[ -n "${repo_meta}" ]]; then
      repo_has_refs="$(
        printf '%s' "${repo_meta}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin)
except Exception:
  print("0")
  raise SystemExit(0)
empty=data.get("empty")
size=data.get("size")
default_branch=(data.get("default_branch") or "").strip()
has_refs=(empty is False) or (isinstance(size,int) and size > 0) or bool(default_branch)
print("1" if has_refs else "0")
PY
      )"
      if [[ "${repo_has_refs}" == "1" && ( -z "${source_repo_branch}" || "${source_repo_branch}" == "HEAD" ) ]]; then
        source_repo_branch="$(
          printf '%s' "${repo_meta}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin)
  print((data.get("default_branch") or "").strip())
except Exception:
  print("")
PY
        )"
      fi
    fi
    case "${skip_remote_when_present}" in
      0|false|FALSE|no|NO|off|OFF) ;;
      *)
        if [[ "${repo_has_refs}" == "1" ]]; then
          echo "[phase60] target repo already exists in Gitea with refs; skipping remote reseed"
          seeded_from_remote="1"
        fi
      ;;
    esac
  fi
  if [[ "${seeded_from_remote}" != "1" && "${trust_gitea_repo}" != "0" && "${trust_gitea_repo}" != "false" && "${trust_gitea_repo}" != "FALSE" && "${trust_gitea_repo}" != "no" && "${trust_gitea_repo}" != "NO" && "${trust_gitea_repo}" != "off" && "${trust_gitea_repo}" != "OFF" ]]; then
    if [[ "${repo_exists}" == "1" && "${repo_has_refs}" == "1" ]]; then
      echo "[phase60] trusting existing Gitea bootstrap repo with refs; skipping remote reseed"
      seeded_from_remote="1"
      source_repo_branch="${source_repo_branch:-${ARGOCD_REPO_BRANCH:-HEAD}}"
    fi
  fi
  if [[ -n "${source_repo_url}" ]]; then
    clone_url="${source_repo_url}"
    if [[ -n "${source_repo_token}" ]] && printf '%s' "${source_repo_url}" | grep -qE '^https://github.com/'; then
      if [[ -z "${source_repo_username}" || "${source_repo_username}" == "oauth2" ]]; then
        if github_token_looks_like_pat "${source_repo_token}"; then
          source_repo_username="$(infer_github_login_from_token "${source_repo_token}" || true)"
          [[ -n "${source_repo_username}" ]] || source_repo_username="$(infer_repo_owner_from_url "${source_repo_url}")"
          [[ -n "${source_repo_username}" ]] || source_repo_username="${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME:-github-user}}"
          source_repo_username_source="${source_repo_username_source:-auto-inferred-pat-login}"
        else
          source_repo_username="x-access-token"
          source_repo_username_source="auto-x-access-token"
        fi
      fi
    elif [[ -z "${source_repo_token}" ]] && printf '%s' "${source_repo_url}" | grep -qE '^https://github.com/'; then
      phase60_log_source_repo_auth "${source_repo_url}" "${source_repo_username}" "${source_repo_token}" "${source_repo_token_source}" "${source_repo_username_source}"
      echo "[phase60] ERROR: GitHub source repo auth missing for ${source_repo_url}" >&2
      echo "[phase60] set GITEA_SEED_SOURCE_TOKEN or provide GITHUB_APP_ID, GITHUB_APP_INSTALLATION_ID, and GITHUB_APP_PRIVATE_KEY_B64 in the runtime payload" >&2
      return 1
    fi
  fi

  phase60_log_source_repo_auth "${source_repo_url}" "${source_repo_username}" "${source_repo_token}" "${source_repo_token_source}" "${source_repo_username_source}"

  if [[ "${seeded_from_remote}" != "1" && -n "${clone_url}" ]]; then
    git_ls_remote_rc=0
    git_clone_rc=0
    if [[ -n "${source_repo_token}" && -n "${source_repo_username}" ]] && printf '%s' "${clone_url}" | grep -qE '^https://'; then
      source_askpass="${tmp_dir}/source-askpass.sh"
      cat >"${source_askpass}" <<'SH'
#!/bin/sh
case "$1" in
  *sername*) printf '%s\n' "${GIT_ASKPASS_USERNAME:-}" ;;
  *) printf '%s\n' "${GIT_ASKPASS_PASSWORD:-}" ;;
esac
SH
      chmod 0700 "${source_askpass}"
      GIT_ASKPASS="${source_askpass}" \
        GIT_ASKPASS_USERNAME="${source_repo_username}" \
        GIT_ASKPASS_PASSWORD="${source_repo_token}" \
        GIT_TERMINAL_PROMPT=0 \
        git ls-remote --symref "${clone_url}" HEAD >/tmp/gitea-seed-head.txt 2>/tmp/gitea-seed-lsremote.err || git_ls_remote_rc=$?
    else
      git ls-remote --symref "${clone_url}" HEAD >/tmp/gitea-seed-head.txt 2>/tmp/gitea-seed-lsremote.err || git_ls_remote_rc=$?
    fi
    if [[ "${git_ls_remote_rc}" == "0" ]]; then
      detected_branch="$(awk '/^ref:/ {sub("^refs/heads/","",$2); print $2; exit}' /tmp/gitea-seed-head.txt)"
      rm -f /tmp/gitea-seed-head.txt /tmp/gitea-seed-lsremote.err
      if [[ -z "${source_repo_branch}" || "${source_repo_branch}" == "HEAD" ]]; then
        source_repo_branch="${detected_branch:-HEAD}"
      fi

      echo "[phase60] seeding Gitea repo from remote source ${source_repo_url}"
      if [[ -n "${source_repo_token}" && -n "${source_repo_username}" ]] && printf '%s' "${clone_url}" | grep -qE '^https://'; then
        GIT_ASKPASS="${source_askpass}" \
          GIT_ASKPASS_USERNAME="${source_repo_username}" \
          GIT_ASKPASS_PASSWORD="${source_repo_token}" \
          GIT_TERMINAL_PROMPT=0 \
          git clone --mirror "${clone_url}" "${tmp_dir}/repo.git" >/dev/null 2>&1 || git_clone_rc=$?
      else
        git clone --mirror "${clone_url}" "${tmp_dir}/repo.git" >/dev/null 2>&1 || git_clone_rc=$?
      fi
      if [[ "${git_clone_rc}" == "0" ]]; then
        cloned_remote_repo="1"
        seeded_from_remote="1"
      else
        echo "[phase60] ERROR: remote repo clone failed for ${source_repo_url}" >&2
        return 1
      fi
    else
      echo "[phase60] ERROR: remote repo probe failed for ${source_repo_url}" >&2
      cat /tmp/gitea-seed-lsremote.err >&2 || true
      rm -f /tmp/gitea-seed-head.txt /tmp/gitea-seed-lsremote.err
      return 1
    fi
  fi

  if [[ "${seeded_from_remote}" != "1" ]]; then
    echo "[phase60] no usable Gitea repo or remote source repo for seed; offline fallback is disabled" >&2
    return 1
  fi

  if [[ -n "${clone_url}" && "${cloned_remote_repo}" == "1" ]]; then
    local git_push_rc=0
    local pushed_refs=""
    askpass="${tmp_dir}/askpass.sh"
    cat >"${askpass}" <<'SH'
#!/bin/sh
case "$1" in
  *sername*) printf '%s\n' "${GIT_ASKPASS_USERNAME:-}" ;;
  *) printf '%s\n' "${GIT_ASKPASS_PASSWORD:-}" ;;
esac
SH
    chmod 0700 "${askpass}"
    (
      cd "${tmp_dir}/repo.git"
      git remote remove gitea >/dev/null 2>&1 || true
      git remote add gitea "${base_url}/${target_owner}/${target_repo}.git"
      GIT_ASKPASS="${askpass}" \
        GIT_ASKPASS_USERNAME="gitea-admin" \
        GIT_ASKPASS_PASSWORD="${admin_token}" \
        GIT_TERMINAL_PROMPT=0 \
        git push --mirror gitea >/dev/null 2>&1
    ) || git_push_rc=$?
    if [[ "${git_push_rc}" != "0" ]]; then
      echo "[phase60] ERROR: failed to mirror-push seeded repo into Gitea" >&2
      return 1
    fi
    pushed_refs="$(
      GIT_ASKPASS="${askpass}" \
        GIT_ASKPASS_USERNAME="gitea-admin" \
        GIT_ASKPASS_PASSWORD="${admin_token}" \
        GIT_TERMINAL_PROMPT=0 \
        git ls-remote "${base_url}/${target_owner}/${target_repo}.git" "refs/heads/*" 2>/dev/null || true
    )"
    if [[ -z "${pushed_refs}" ]]; then
      echo "[phase60] ERROR: mirrored repo push completed but Gitea still exposes no readable refs" >&2
      return 1
    fi
  fi

  echo "[phase60] bootstrap repo auto-render disabled; using seeded repo state as-is"

  write_secret_file argocd_repo_url "${argocd_git_url}"
  gitea_git_token="$(gitea_git_token_effective || true)"
  gitea_repo_username_for_git="${gitea_repo_username_effective:-gitea-admin}"
  write_secret_file argocd_repo_username "${gitea_repo_username_for_git}"
  write_secret_file argocd_repo_token "${gitea_git_token:-${admin_token}}"
  write_secret_file argocd_repo_branch "${source_repo_branch:-HEAD}"
  if [[ -n "${clone_url}" && "${seeded_from_remote}" == "1" ]]; then
    echo "[phase60] Gitea repo seeded -> ${gitea_git_url}"
  else
    echo "[phase60] Gitea repo selected -> ${gitea_git_url}"
  fi
  echo "[phase60] Argo CD repo source -> ${argocd_git_url}"
}

fail_local_requirement() {
  if [[ "${PHASE60_WARNING_ONLY}" == "1" ]]; then
    echo "[phase60] WARNING: $*" >&2
    bootstrap_diag_record \
      "phase=phase60" \
      "step=${PHASE60_CURRENT_STEP:-phase60}" \
      "component=${PHASE60_CURRENT_COMPONENT:-phase60}" \
      "operation=requirement-warning" \
      "severity=warning" \
      "failure_kind=$(bootstrap_diag_classify_failure_kind "$*")" \
      "summary=$*" \
      "log_path=${BOOTSTRAP_DIAG_LOG_PATH}" \
      "realization=$([[ "${PHASE60_MODE}" == "realize" ]] && echo true || echo false)"
    return 0
  fi
  echo "[phase60] ERROR: $*" >&2
  bootstrap_diag_record \
    "phase=phase60" \
    "step=${PHASE60_CURRENT_STEP:-phase60}" \
    "component=${PHASE60_CURRENT_COMPONENT:-phase60}" \
    "operation=requirement-failed" \
    "severity=error" \
    "failure_kind=$(bootstrap_diag_classify_failure_kind "$*")" \
    "summary=$*" \
    "log_path=${BOOTSTRAP_DIAG_LOG_PATH}" \
    "realization=$([[ "${PHASE60_MODE}" == "realize" ]] && echo true || echo false)"
  exit 1
}

run_or_fail() {
  local desc="${1:?description}"
  shift
  PHASE60_CURRENT_STEP="${desc}"
  if ! "$@"; then
    fail_local_requirement "${desc}"
  fi
}

phase60_component_debug_dump() {
  local component="${1:?component}"
  local reason="${2:-unknown}"
  local log_path="${3:?log_path}"
  local evidence_path=""
  shift 3

  evidence_path="$(bootstrap_diag_capture_evidence_path "${component}" "${reason}")"
  mkdir -p "$(dirname "${log_path}")" 2>/dev/null || true
  {
    "$@"
  } >>"${log_path}" 2>&1
  cat "${log_path}" >/dev/null 2>&1 || true
  if [[ -f "${log_path}" ]]; then
    tail -n 400 "${log_path}" >"${evidence_path}" 2>/dev/null || cp -f "${log_path}" "${evidence_path}" 2>/dev/null || true
  fi
  bootstrap_diag_record_file_event \
    "${component}" \
    "${component}-${reason}" \
    "captured ${component} diagnostics (${reason})" \
    "${evidence_path}:${log_path}" \
    "warning" \
    "kubernetes-api"
}

phase60_run_start_recorded=0
phase60_exit_trap() {
  local rc=$?
  local end_ts duration
  end_ts="$(date +%s)"
  duration="$((end_ts - phase60_start_ts))"
  if [[ "${rc}" -eq 0 ]]; then
    bootstrap_diag_record \
      "phase=phase60" \
      "step=${PHASE60_MODE}" \
      "component=phase60" \
      "operation=run-complete" \
      "severity=info" \
      "exit_code=0" \
      "duration_seconds=${duration}" \
      "summary=phase60 ${PHASE60_MODE} complete" \
      "log_path=${BOOTSTRAP_DIAG_LOG_PATH}" \
      "realization=$([[ "${PHASE60_MODE}" == "realize" ]] && echo true || echo false)"
  else
    bootstrap_diag_record \
      "phase=phase60" \
      "step=${PHASE60_CURRENT_STEP:-${PHASE60_MODE}}" \
      "component=${PHASE60_CURRENT_COMPONENT:-phase60}" \
      "operation=run-failed" \
      "severity=error" \
      "exit_code=${rc}" \
      "duration_seconds=${duration}" \
      "failure_kind=$(bootstrap_diag_classify_failure_kind "${phase60_last_error_cmd}")" \
      "summary=phase60 ${PHASE60_MODE} failed at line ${phase60_last_error_line:-unknown}: ${phase60_last_error_cmd:-unknown}" \
      "log_path=${BOOTSTRAP_DIAG_LOG_PATH}" \
      "realization=$([[ "${PHASE60_MODE}" == "realize" ]] && echo true || echo false)"
  fi
  exit "${rc}"
}
trap 'phase60_exit_trap' EXIT
trap 'phase60_last_error_line="${BASH_LINENO[0]:-unknown}"; phase60_last_error_cmd="${BASH_COMMAND:-unknown}"' ERR

gitea_debug_dump() {
  local reason="${1:-unknown}"
  local ts=""
  local pod=""
  local cdesc=""
  local cname=""
  ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)"

  mkdir -p "$(dirname "${PHASE60_GITEA_DEBUG_LOG}")" 2>/dev/null || true
  {
    echo ""
    echo "===== phase60 gitea debug @ ${ts} (${reason}) ====="
    echo "[kubectl] gitea resources"
    "${kubectl_bin}" -n gitea get deploy gitea -o wide 2>/dev/null || true
    "${kubectl_bin}" -n gitea get rs -o wide 2>/dev/null || true
    "${kubectl_bin}" -n gitea get pods -o wide 2>/dev/null || true
    "${kubectl_bin}" -n gitea get svc "${GITEA_INTERNAL_SERVICE_NAME}" -o wide 2>/dev/null || true
    "${kubectl_bin}" -n gitea get endpoints "${GITEA_INTERNAL_SERVICE_NAME}" -o yaml 2>/dev/null || true
    echo "[kubectl] describe deployment"
    "${kubectl_bin}" -n gitea describe deploy gitea 2>/dev/null || true
    echo "[kubectl] recent events"
    "${kubectl_bin}" -n gitea get events --sort-by=.lastTimestamp 2>/dev/null | tail -n 80 || true
    for pod in $("${kubectl_bin}" -n gitea get pods -o name 2>/dev/null); do
      echo "[kubectl] describe ${pod}"
      "${kubectl_bin}" -n gitea describe "${pod}" 2>/dev/null || true
      echo "[kubectl] logs ${pod} (all containers, tail=200)"
      "${kubectl_bin}" -n gitea logs "${pod}" --all-containers --tail=200 2>/dev/null || true
      echo "[kubectl] logs ${pod} (per-container current+previous, tail=200)"
      while IFS= read -r cdesc; do
        [[ -n "${cdesc}" ]] || continue
        cname="${cdesc#*:}"
        [[ -n "${cname}" ]] || continue
        echo "  [container=${cname}] current"
        "${kubectl_bin}" -n gitea logs "${pod}" -c "${cname}" --tail=200 2>/dev/null || true
        echo "  [container=${cname}] previous"
        "${kubectl_bin}" -n gitea logs "${pod}" -c "${cname}" --previous --tail=200 2>/dev/null || true
      done < <(
        "${kubectl_bin}" -n gitea get "${pod}" \
          -o jsonpath='{range .spec.initContainers[*]}init:{.name}{"\n"}{end}{range .spec.containers[*]}main:{.name}{"\n"}{end}' 2>/dev/null || true
      )
    done
    echo "[kubectl] coredns resources"
    "${kubectl_bin}" -n kube-system get deploy,svc 2>/dev/null | grep -E 'coredns|kube-dns' || true
    "${kubectl_bin}" -n kube-system get pods -o wide 2>/dev/null | grep -E 'coredns|kube-dns' || true
    echo "[kubectl] coredns recent events"
    "${kubectl_bin}" -n kube-system get events --sort-by=.lastTimestamp 2>/dev/null | grep -E 'coredns|kube-dns|dns' | tail -n 80 || true
    for pod in $("${kubectl_bin}" -n kube-system get pods -o name 2>/dev/null | grep -E 'coredns|kube-dns' || true); do
      echo "[kubectl] logs ${pod} (tail=200)"
      "${kubectl_bin}" -n kube-system logs "${pod}" --all-containers --tail=200 2>/dev/null || true
      echo "[kubectl] logs ${pod} previous (tail=200)"
      "${kubectl_bin}" -n kube-system logs "${pod}" --all-containers --previous --tail=200 2>/dev/null || true
    done
    echo "===== end phase60 gitea debug ====="
  } >>"${PHASE60_GITEA_DEBUG_LOG}" 2>&1
  bootstrap_diag_record_file_event \
    "gitea" \
    "gitea-debug" \
    "captured gitea diagnostics (${reason})" \
    "${PHASE60_GITEA_DEBUG_LOG}" \
    "warning" \
    "rollout"
}

cloudflared_debug_dump() {
  local reason="${1:-unknown}"
  local ts=""
  local pod=""
  ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)"

  mkdir -p "$(dirname "${PHASE60_CLOUDFLARED_DEBUG_LOG}")" 2>/dev/null || true
  {
    echo ""
    echo "===== phase60 cloudflared debug @ ${ts} (${reason}) ====="
    "${kubectl_bin}" -n cloudflared get deploy,pods,svc -o wide 2>/dev/null || true
    "${kubectl_bin}" -n cloudflared get events --sort-by=.lastTimestamp 2>/dev/null | tail -n 80 || true
    "${kubectl_bin}" -n cloudflared describe deploy cloudflared 2>/dev/null || true
    for pod in $("${kubectl_bin}" -n cloudflared get pods -o name 2>/dev/null); do
      echo "[kubectl] describe ${pod}"
      "${kubectl_bin}" -n cloudflared describe "${pod}" 2>/dev/null || true
      echo "[kubectl] logs ${pod}"
      "${kubectl_bin}" -n cloudflared logs "${pod}" --all-containers --tail=200 2>/dev/null || true
      echo "[kubectl] logs ${pod} previous"
      "${kubectl_bin}" -n cloudflared logs "${pod}" --all-containers --previous --tail=200 2>/dev/null || true
    done
    echo "===== end phase60 cloudflared debug ====="
  } >>"${PHASE60_CLOUDFLARED_DEBUG_LOG}" 2>&1
  bootstrap_diag_record_file_event \
    "cloudflared" \
    "cloudflared-debug" \
    "captured cloudflared diagnostics (${reason})" \
    "${PHASE60_CLOUDFLARED_DEBUG_LOG}" \
    "warning" \
    "rollout"
}

rancher_debug_dump() {
  local reason="${1:-unknown}"
  local ts=""
  local pod=""
  ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)"

  mkdir -p "$(dirname "${PHASE60_RANCHER_DEBUG_LOG}")" 2>/dev/null || true
  {
    echo ""
    echo "===== phase60 rancher debug @ ${ts} (${reason}) ====="
    "${kubectl_bin}" -n cattle-system get deploy,svc,endpoints,pods,ingress -o wide 2>/dev/null || true
    "${kubectl_bin}" -n cattle-system get events --sort-by=.lastTimestamp 2>/dev/null | tail -n 80 || true
    "${kubectl_bin}" -n cattle-system describe deploy rancher 2>/dev/null || true
    for pod in $("${kubectl_bin}" -n cattle-system get pods -l app=rancher -o name 2>/dev/null); do
      echo "[kubectl] describe ${pod}"
      "${kubectl_bin}" -n cattle-system describe "${pod}" 2>/dev/null || true
      echo "[kubectl] logs ${pod}"
      "${kubectl_bin}" -n cattle-system logs "${pod}" --all-containers --tail=200 2>/dev/null || true
      echo "[kubectl] logs ${pod} previous"
      "${kubectl_bin}" -n cattle-system logs "${pod}" --all-containers --previous --tail=200 2>/dev/null || true
    done
    echo "===== end phase60 rancher debug ====="
  } >>"${PHASE60_RANCHER_DEBUG_LOG}" 2>&1
  bootstrap_diag_record_file_event \
    "rancher" \
    "rancher-debug" \
    "captured rancher diagnostics (${reason})" \
    "${PHASE60_RANCHER_DEBUG_LOG}" \
    "warning" \
    "rollout"
}

wait_for_gitea_rollout() {
  local attempts="${PHASE60_GITEA_ROLLOUT_ATTEMPTS}"
  local attempt=1
  while (( attempt <= attempts )); do
    if "${kubectl_bin}" -n gitea rollout status deployment/gitea --timeout="${PHASE60_GITEA_ROLLOUT_TIMEOUT}"; then
      return 0
    fi

    echo "[phase60] warning: gitea rollout attempt ${attempt}/${attempts} failed"
    gitea_debug_dump "rollout-attempt-${attempt}-failed"

    if (( attempt < attempts )); then
      if [[ "${PHASE60_GITEA_ROLLOUT_RESTART_ON_FAIL}" == "1" || "${PHASE60_GITEA_ROLLOUT_RESTART_ON_FAIL}" == "true" ]]; then
        echo "[phase60] warning: restarting deployment/gitea before retry"
        "${kubectl_bin}" -n gitea rollout restart deployment/gitea >/dev/null 2>&1 || true
      fi
      sleep 10
    fi

    attempt=$((attempt + 1))
  done

  return 1
}

wait_for_cloudflared_rollout() {
  local attempts="${PHASE60_CLOUDFLARED_ROLLOUT_ATTEMPTS}"
  local attempt=1
  while (( attempt <= attempts )); do
    if "${kubectl_bin}" -n cloudflared rollout status deployment/cloudflared --timeout="${PHASE60_CLOUDFLARED_ROLLOUT_TIMEOUT}"; then
      return 0
    fi

    echo "[phase60] warning: cloudflared rollout attempt ${attempt}/${attempts} failed"
    cloudflared_debug_dump "rollout-attempt-${attempt}-failed"

    if (( attempt < attempts )); then
      if [[ "${PHASE60_CLOUDFLARED_ROLLOUT_RESTART_ON_FAIL}" == "1" || "${PHASE60_CLOUDFLARED_ROLLOUT_RESTART_ON_FAIL}" == "true" ]]; then
        echo "[phase60] warning: restarting deployment/cloudflared before retry"
        "${kubectl_bin}" -n cloudflared rollout restart deployment/cloudflared >/dev/null 2>&1 || true
      fi
      sleep 10
    fi

    attempt=$((attempt + 1))
  done

  return 1
}

require_cloudflared_ready() {
  if ! "${kubectl_bin}" get namespace cloudflared >/dev/null 2>&1; then
    return 0
  fi
  if ! "${kubectl_bin}" -n cloudflared get deployment cloudflared >/dev/null 2>&1; then
    return 0
  fi
  if ! wait_for_cloudflared_rollout; then
    fail_local_requirement \
      "cloudflared deployment did not become ready within ${PHASE60_CLOUDFLARED_ROLLOUT_TIMEOUT} (attempts=${PHASE60_CLOUDFLARED_ROLLOUT_ATTEMPTS}); see ${PHASE60_CLOUDFLARED_DEBUG_LOG}"
  fi
  echo "[phase60] cloudflared rollout checks passed"
}

detect_rancher_desired_replicas() {
  local requested="${PHASE60_RANCHER_DESIRED_REPLICAS}"
  local ready_count=""

  if printf '%s' "${requested}" | grep -Eq '^[0-9]+$' && [[ "${requested}" -ge 1 ]]; then
    printf '%s' "${requested}"
    return 0
  fi

  ready_count="$("${kubectl_bin}" get nodes --no-headers 2>/dev/null | awk '$2 ~ /^Ready/ {c++} END{print c+0}' || true)"
  if ! printf '%s' "${ready_count}" | grep -Eq '^[0-9]+$'; then
    ready_count=1
  fi
  if [[ "${ready_count}" -lt 1 ]]; then
    ready_count=1
  fi
  if [[ "${ready_count}" -gt 3 ]]; then
    ready_count=3
  fi
  printf '%s' "${ready_count}"
}

tune_rancher_deployment() {
  local replicas=""
  if ! "${kubectl_bin}" get namespace cattle-system >/dev/null 2>&1; then
    return 0
  fi
  if ! "${kubectl_bin}" -n cattle-system get deployment rancher >/dev/null 2>&1; then
    return 0
  fi

  replicas="$(detect_rancher_desired_replicas)"
  echo "[phase60] tuning rancher deployment (replicas=${replicas}, startupFailureThreshold=${PHASE60_RANCHER_STARTUP_FAILURE_THRESHOLD})"
  "${kubectl_bin}" -n cattle-system patch deployment rancher --type=strategic -p "{
    \"spec\": {
      \"replicas\": ${replicas},
      \"progressDeadlineSeconds\": ${PHASE60_RANCHER_PROGRESS_DEADLINE_SECONDS},
      \"template\": {
        \"spec\": {
          \"containers\": [
            {
              \"name\": \"rancher\",
              \"startupProbe\": {
                \"httpGet\": {\"path\": \"/healthz\", \"port\": 80, \"scheme\": \"HTTP\"},
                \"timeoutSeconds\": ${PHASE60_RANCHER_STARTUP_TIMEOUT_SECONDS},
                \"periodSeconds\": ${PHASE60_RANCHER_STARTUP_PERIOD_SECONDS},
                \"failureThreshold\": ${PHASE60_RANCHER_STARTUP_FAILURE_THRESHOLD},
                \"successThreshold\": 1
              }
            }
          ]
        }
      }
    }
  }" >/dev/null
}

require_rancher_origin_ready() {
  local eps_ips=""
  local svc_ip=""
  local probe_host=""
  local health_code=""

  if ! "${kubectl_bin}" get namespace cattle-system >/dev/null 2>&1; then
    return 0
  fi
  if ! "${kubectl_bin}" -n cattle-system get deployment rancher >/dev/null 2>&1; then
    return 0
  fi

  tune_rancher_deployment

  if ! "${kubectl_bin}" -n cattle-system rollout status deployment/rancher --timeout="${PHASE60_RANCHER_ROLLOUT_TIMEOUT}"; then
    rancher_debug_dump "rollout-timeout"
    fail_local_requirement \
      "rancher deployment did not become ready within ${PHASE60_RANCHER_ROLLOUT_TIMEOUT}; see ${PHASE60_RANCHER_DEBUG_LOG}"
  fi

  eps_ips="$("${kubectl_bin}" -n cattle-system get endpoints rancher -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null || true)"
  if [[ -z "${eps_ips}" ]]; then
    rancher_debug_dump "no-endpoints"
    fail_local_requirement "rancher service has no ready endpoints (service=rancher namespace=cattle-system)"
  fi

  svc_ip="$("${kubectl_bin}" -n cattle-system get svc rancher -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true)"
  if [[ -n "${svc_ip}" && "${svc_ip}" != "None" ]]; then
    probe_host="${svc_ip}"
  else
    probe_host="$(printf '%s' "${eps_ips}" | awk '{print $1}')"
  fi
  if [[ -z "${probe_host}" ]]; then
    rancher_debug_dump "probe-host-missing"
    fail_local_requirement "unable to determine probe host for rancher origin health check"
  fi

  health_code="$(curl -k -sS -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 20 \
    "https://${probe_host}/healthz" || true)"
  if [[ "${health_code}" != "200" && "${health_code}" != "301" && "${health_code}" != "302" ]]; then
    rancher_debug_dump "healthz-http-${health_code:-empty}"
    fail_local_requirement \
      "rancher origin health check failed via ${probe_host} (http=${health_code:-<empty>}, expected 200/301/302)"
  fi

  echo "[phase60] rancher origin checks passed"
}

require_gitea_golden_path() {
  local health_code=""
  local asset_code=""
  local pod_name=""
  local pf_pid=""
  local pf_port=""
  local pf_log=""
  local available_replicas="0"
  local ready_replicas="0"

  run_or_fail \
    "gitea deployment not found (namespace gitea, deployment/gitea)" \
    "${kubectl_bin}" -n gitea get deployment gitea

  # Phase 50 frequently lands while image pulls / PV attach are still converging.
  # Extend deployment progress deadline so rollout status does not fail prematurely.
  "${kubectl_bin}" -n gitea patch deployment gitea --type merge \
    -p "{\"spec\":{\"progressDeadlineSeconds\":${PHASE60_GITEA_PROGRESS_DEADLINE_SECONDS}}}" >/dev/null 2>&1 || true

  if ! wait_for_gitea_rollout; then
    available_replicas="$("${kubectl_bin}" -n gitea get deployment gitea -o jsonpath='{.status.availableReplicas}' 2>/dev/null || true)"
    ready_replicas="$("${kubectl_bin}" -n gitea get deployment gitea -o jsonpath='{.status.readyReplicas}' 2>/dev/null || true)"
    available_replicas="${available_replicas:-0}"
    ready_replicas="${ready_replicas:-0}"
    if [[ "${available_replicas}" -lt 1 || "${ready_replicas}" -lt 1 ]]; then
      fail_local_requirement \
        "gitea deployment did not become ready within ${PHASE60_GITEA_ROLLOUT_TIMEOUT} (attempts=${PHASE60_GITEA_ROLLOUT_ATTEMPTS}); see ${PHASE60_GITEA_DEBUG_LOG}"
    fi
    echo "[phase60] warning: gitea rollout did not fully settle, but deployment has ready replicas (ready=${ready_replicas} available=${available_replicas}); continuing with API health checks"
  fi

  for selector in \
    'app.kubernetes.io/instance=gitea,app.kubernetes.io/name=gitea' \
    'app=gitea'
  do
    pod_name="$("${kubectl_bin}" -n gitea get pod -l "${selector}" --field-selector=status.phase=Running \
      -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.conditions[?(@.type=="Ready")]}{.status}{"\n"}{end}{end}' 2>/dev/null \
      | awk '$2 == "True" {print $1; exit}' || true)"
    if [[ -n "${pod_name}" ]]; then
      break
    fi
  done
  if [[ -z "${pod_name}" ]]; then
    fail_local_requirement "could not find a ready Gitea pod for API health checks"
  fi

  pf_port="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
  pf_log="$(mktemp /tmp/gitea-portforward.XXXXXX.log)"
  "${kubectl_bin}" -n gitea port-forward "pod/${pod_name}" "${pf_port}:3000" >"${pf_log}" 2>&1 &
  pf_pid=$!
  sleep 2
  health_code="$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 15 \
    "http://127.0.0.1:${pf_port}/api/v1/version" || true)"
  asset_code="$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 15 \
    "http://127.0.0.1:${pf_port}/assets/js/index.js" || true)"
  kill "${pf_pid}" >/dev/null 2>&1 || true
  wait "${pf_pid}" >/dev/null 2>&1 || true
  rm -f "${pf_log}" >/dev/null 2>&1 || true
  if [[ "${health_code}" != "200" ]]; then
    fail_local_requirement "gitea API probe failed via pod/${pod_name} (http=${health_code:-<empty>}, expected 200)"
  fi

  echo "[phase60] gitea golden-path health checks passed"
  if [[ "${asset_code}" != "200" ]]; then
    fail_local_requirement "gitea asset probe failed via pod/${pod_name} (http=${asset_code:-<empty>}, expected 200)"
  fi
}

resolve_host_ips() {
  local host="${1:?host}"

  if command -v getent >/dev/null 2>&1; then
    getent ahostsv4 "${host}" 2>/dev/null | awk 'NF {print $1}' | sort -u
    return 0
  fi

  if command -v host >/dev/null 2>&1; then
    host "${host}" 2>/dev/null | awk '/has address/ {print $NF}' | sort -u
    return 0
  fi

  fail_local_requirement "no DNS lookup tool available (need getent or host) to validate .local domain access"
}

require_local_domain_access() {
  local host="${1:?host}"
  local expected_vip="${2:-}"
  local resolved_ips=""
  local http_code=""
  local https_code=""
  local success_scheme="http"
  PHASE60_LAST_HTTP_CODE=""

  resolved_ips="$(resolve_host_ips "${host}" | tr '\n' ' ' | xargs || true)"
  if [[ -z "${resolved_ips}" ]]; then
    echo "[phase60] ${host} does not resolve on this node" >&2
    return 1
  fi

  if [[ -n "${expected_vip}" ]]; then
    if ! printf '%s\n' ${resolved_ips} | grep -Fx "${expected_vip}" >/dev/null 2>&1; then
      echo "[phase60] ${host} resolves to [${resolved_ips}], expected ingress VIP ${expected_vip}" >&2
      return 1
    fi
  fi

  http_code="$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 10 "http://${host}/" || true)"
  if [[ -z "${http_code}" || "${http_code}" = "000" ]]; then
    https_code="$(curl -k -sS -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 10 "https://${host}/" || true)"
    if [[ -n "${https_code}" && "${https_code}" != "000" ]]; then
      http_code="${https_code}"
      success_scheme="https"
    else
      PHASE60_LAST_HTTP_CODE="${http_code:-${https_code}}"
      echo "[phase60] ${host} resolved but is not reachable over HTTP or HTTPS" >&2
      return 1
    fi
  fi
  PHASE60_LAST_HTTP_CODE="${http_code}"
  if ! printf '%s' "${http_code}" | grep -Eq "${PHASE60_DOMAIN_HTTP_SUCCESS_REGEX}"; then
    echo "[phase60] ${host} returned unexpected HTTP status ${http_code}" >&2
    return 2
  fi

  echo "[phase60] verified ${host} (ips=${resolved_ips}, scheme=${success_scheme}, http=${http_code})"
  return 0
}

check_domain_with_retry() {
  local host="${1:?host}"
  local expected_vip="${2:-}"
  local retries="${3:-${PHASE60_DOMAIN_CHECK_RETRIES}}"
  local delay="${PHASE60_DOMAIN_CHECK_DELAY}"
  local i=1
  local last_rc=1

  if ! printf '%s' "${retries}" | grep -Eq '^[0-9]+$'; then
    retries=1
  fi
  if ! printf '%s' "${delay}" | grep -Eq '^[0-9]+$'; then
    delay=5
  fi
  if [[ "${retries}" -lt 1 ]]; then
    retries=1
  fi
  if [[ "${delay}" -lt 1 ]]; then
    delay=1
  fi

  while [[ "${i}" -le "${retries}" ]]; do
    if require_local_domain_access "${host}" "${expected_vip}"; then
      return 0
    else
      last_rc=$?
    fi
    if [[ "${i}" -lt "${retries}" ]]; then
      echo "[phase60] domain check retry ${i}/${retries} for ${host} in ${delay}s" >&2
      sleep "${delay}"
    fi
    i="$((i + 1))"
  done
  return "${last_rc}"
}

emit_dns_update_window_notice() {
  local vip="${1:-}"
  local retries="${2:-${PHASE60_DOMAIN_CHECK_RETRIES}}"
  local delay="${3:-${PHASE60_DOMAIN_CHECK_DELAY}}"
  local total_seconds=0
  local total_minutes=0

  if ! printf '%s' "${retries}" | grep -Eq '^[0-9]+$'; then
    retries=0
  fi
  if ! printf '%s' "${delay}" | grep -Eq '^[0-9]+$'; then
    delay=0
  fi

  total_seconds=$(( retries * delay ))
  total_minutes=$(( (total_seconds + 59) / 60 ))

  if [[ -n "${vip}" ]]; then
    echo "[phase60] ingress VIP pinned at ${vip}"
  fi
  if (( total_seconds > 0 )); then
    echo "[phase60] waiting up to ${total_seconds}s (~${total_minutes}m) for DNS to converge; pull this log, update DNS to the pinned VIP above, and let bootstrap continue during that window"
  else
    echo "[phase60] DNS convergence wait is enabled; pull this log, update DNS to the pinned VIP above, and let bootstrap continue"
  fi
}

local_domain_retry_count() {
  if [[ "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "1" || "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "true" ]]; then
    printf '%s' "${PHASE60_DOMAIN_CHECK_RETRIES}"
  else
    printf '%s' "${PHASE60_LOCAL_DOMAIN_CHECK_RETRIES:-3}"
  fi
}

gitea_local_domain_retry_count() {
  local retries="${PHASE60_GITEA_LOCAL_DOMAIN_CHECK_RETRIES:-}"
  if [[ -z "${retries}" ]]; then
    if [[ "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "1" || "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "true" ]]; then
      retries="${PHASE60_DOMAIN_CHECK_RETRIES}"
    else
      retries=1
    fi
  fi
  if ! printf '%s' "${retries}" | grep -Eq '^[0-9]+$'; then
    retries=1
  fi
  if [[ "${retries}" -lt 1 ]]; then
    retries=1
  fi
  printf '%s' "${retries}"
}

derive_local_domain() {
  local domain="${1:-}"
  python3 - <<'PY' "${domain}"
import sys
d=(sys.argv[1] if len(sys.argv) > 1 else "").strip().lower().strip(".")
if not d:
    print("local")
    raise SystemExit(0)
parts=[p for p in d.split(".") if p]
if len(parts) <= 1:
    print(f"{parts[0]}.local" if parts else "local")
else:
    print(".".join(parts[:-1] + ["local"]))
PY
}

normalize_gitea_repo_url() {
  local repo_url="${1:-}"
  python3 - <<'PY' "${repo_url}" "${GITEA_INTERNAL_URL}" "${GITEA_INTERNAL_SERVICE_NAME}"
import sys
from urllib.parse import urlparse

repo_url = (sys.argv[1] if len(sys.argv) > 1 else "").strip()
internal_base = (sys.argv[2] if len(sys.argv) > 2 else "").strip()
service_name = (sys.argv[3] if len(sys.argv) > 3 else "").strip().lower()
if not repo_url or not internal_base:
    print(repo_url, end="")
    raise SystemExit(0)

parsed = urlparse(repo_url)
host = (parsed.hostname or "").strip().lower()
path = (parsed.path or "").lstrip("/")

known_gitea_hosts = (
    host.startswith("gitea.")
    or host == f"{service_name}.gitea.svc"
    or host.startswith(f"{service_name}.gitea.svc.")
    or host == "127.0.0.1"
    or all(part.isdigit() for part in host.split(".") if part) and host.count(".") == 3
)

if known_gitea_hosts and path.endswith(".git"):
    print(internal_base.rstrip("/") + "/" + path, end="")
else:
    print(repo_url, end="")
PY
}

if [[ -z "${CLUSTER_LOCAL_DOMAIN}" ]]; then
  CLUSTER_LOCAL_DOMAIN="$(derive_local_domain "${CLUSTER_DOMAIN}")"
fi

if [[ -z "${GITEA_CANONICAL_URL}" ]]; then
  GITEA_CANONICAL_URL="http://gitea.${CLUSTER_LOCAL_DOMAIN}/"
fi

find_routing_frontdoor_service() {
  if "${kubectl_bin}" -n kube-system get svc rke2-ingress-nginx-controller >/dev/null 2>&1; then
    printf 'kube-system/rke2-ingress-nginx-controller'
    return 0
  fi
  if "${kubectl_bin}" -n ingress-nginx get svc ingress-nginx-controller >/dev/null 2>&1; then
    printf 'ingress-nginx/ingress-nginx-controller'
    return 0
  fi
  local ns="" service_name=""
  for ns in kube-system ingress-nginx; do
    service_name="$(
      "${kubectl_bin}" -n "${ns}" get svc \
        -l 'app.kubernetes.io/name=ingress-nginx,app.kubernetes.io/component=controller' \
        -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
        | head -n1 | tr -d '\r\n'
    )"
    if [[ -n "${service_name}" ]]; then
      printf '%s/%s' "${ns}" "${service_name}"
      return 0
    fi
  done
  return 1
}

detect_ingress_controller_workload() {
  local kind="" ns="" name="" line=""
  for kind in ds deploy; do
    line="$(
      "${kubectl_bin}" get "${kind}" -A \
        -l 'app.kubernetes.io/name=ingress-nginx,app.kubernetes.io/component=controller' \
        -o jsonpath='{range .items[*]}{.metadata.namespace}{"|"}{.metadata.name}{"\n"}{end}' 2>/dev/null \
        | head -n1 | tr -d '\r'
    )"
    if [[ -n "${line}" ]]; then
      ns="${line%%|*}"
      name="${line##*|}"
      printf '%s|%s|%s' "${kind}" "${ns}" "${name}"
      return 0
    fi
  done
  return 1
}

detect_ingress_controller_pod() {
  local line=""
  line="$(
    "${kubectl_bin}" get pods -A \
      -l 'app.kubernetes.io/name=ingress-nginx,app.kubernetes.io/component=controller' \
      -o jsonpath='{range .items[*]}{.metadata.namespace}{"|"}{.metadata.name}{"\n"}{end}' 2>/dev/null \
      | head -n1 | tr -d '\r'
  )"
  if [[ -n "${line}" ]]; then
    printf '%s' "${line}"
    return 0
  fi

  line="$(
    "${kubectl_bin}" get pods -A \
      -o jsonpath='{range .items[*]}{.metadata.namespace}{"|"}{.metadata.name}{"\n"}{end}' 2>/dev/null \
      | grep -E '^(kube-system|ingress-nginx)\|(rke2-)?ingress-nginx-controller([-].+)?$' \
      | head -n1 | tr -d '\r'
  )"
  if [[ -n "${line}" ]]; then
    printf '%s' "${line}"
    return 0
  fi

  return 1
}

extract_ingress_controller_selector_yaml() {
  local workload_kind="$1"
  local workload_ns="$2"
  local workload_name="$3"
  "${kubectl_bin}" -n "${workload_ns}" get "${workload_kind}" "${workload_name}" \
    -o jsonpath='{range $k,$v := .spec.selector.matchLabels}{"    "}{$k}{": "}{$v}{"\n"}{end}' 2>/dev/null \
    | tr -d '\r'
}

extract_ingress_controller_selector_yaml_from_pod() {
  local pod_ns="$1"
  local pod_name="$2"
  local key="" value="" selector_yaml=""
  for key in app.kubernetes.io/name app.kubernetes.io/component app.kubernetes.io/instance app.kubernetes.io/part-of; do
    value="$(
      "${kubectl_bin}" -n "${pod_ns}" get pod "${pod_name}" \
        -o jsonpath="{.metadata.labels['${key//./\\.}']}" 2>/dev/null \
        | tr -d '\r'
    )"
    if [[ -n "${value}" ]]; then
      selector_yaml="${selector_yaml}    ${key}: ${value}"$'\n'
    fi
  done
  printf '%s' "${selector_yaml}"
}

ensure_routing_frontdoor_service() {
  local existing="" workload="" workload_kind="" workload_ns="" workload_name="" service_name="" selector_yaml="" pod_ref="" pod_ns="" pod_name=""
  existing="$(find_routing_frontdoor_service || true)"
  if [[ -n "${existing}" ]]; then
    printf '%s' "${existing}"
    return 0
  fi

  workload="$(detect_ingress_controller_workload || true)"
  if [[ -n "${workload}" ]]; then
    workload_kind="${workload%%|*}"
    workload="${workload#*|}"
    workload_ns="${workload%%|*}"
    workload_name="${workload##*|}"
    service_name="${workload_name}"
    if [[ "${workload_ns}" == "ingress-nginx" ]]; then
      service_name="ingress-nginx-controller"
    elif [[ "${workload_ns}" == "kube-system" ]]; then
      service_name="rke2-ingress-nginx-controller"
    fi
    selector_yaml="$(extract_ingress_controller_selector_yaml "${workload_kind}" "${workload_ns}" "${workload_name}")"
  fi
  if [[ -z "${selector_yaml}" ]]; then
    pod_ref="$(detect_ingress_controller_pod || true)"
    if [[ -n "${pod_ref}" ]]; then
      pod_ns="${pod_ref%%|*}"
      pod_name="${pod_ref##*|}"
      workload_kind="pod"
      workload_ns="${pod_ns}"
      workload_name="${pod_name}"
      service_name="${pod_name}"
      if [[ "${workload_ns}" == "ingress-nginx" ]]; then
        service_name="ingress-nginx-controller"
      elif [[ "${workload_ns}" == "kube-system" ]]; then
        service_name="rke2-ingress-nginx-controller"
      fi
      selector_yaml="$(extract_ingress_controller_selector_yaml_from_pod "${pod_ns}" "${pod_name}")"
    fi
  fi
  if [[ -z "${workload_ns}" || -z "${service_name}" ]]; then
    return 1
  fi
  if [[ -z "${selector_yaml}" ]]; then
    echo "[phase60] warning: unable to derive ${workload_kind}/${workload_name} selector; using generic ingress-nginx controller selector" >&2
    selector_yaml=$'    app.kubernetes.io/name: ingress-nginx\n    app.kubernetes.io/component: controller'
  fi

  echo "[phase60] creating authoritative ingress front-door Service ${workload_ns}/${service_name} for ${workload_kind}/${workload_name}" >&2
  cat <<EOF | "${kubectl_bin}" -n "${workload_ns}" apply -f - >/dev/null
apiVersion: v1
kind: Service
metadata:
  name: ${service_name}
  labels:
    app.kubernetes.io/name: ingress-nginx
    app.kubernetes.io/component: controller
    bootstrap.cluster.duck/managed-by: phase60
spec:
  type: ClusterIP
  selector:
${selector_yaml}
  ports:
    - name: http
      port: 80
      protocol: TCP
      targetPort: http
    - name: https
      port: 443
      protocol: TCP
      targetPort: https
EOF

  existing="$(find_routing_frontdoor_service || true)"
  if [[ -n "${existing}" ]]; then
    printf '%s' "${existing}"
    return 0
  fi
  return 1
}

wait_for_routing_frontdoor_service() {
  local timeout="${PHASE60_INGRESS_FRONTDOOR_WAIT_TIMEOUT:-300}"
  local delay="${PHASE60_INGRESS_FRONTDOOR_WAIT_DELAY:-5}"
  local elapsed=0
  local found=""

  while [[ "${elapsed}" -le "${timeout}" ]]; do
    found="$(ensure_routing_frontdoor_service || true)"
    if [[ -n "${found}" ]]; then
      printf '%s' "${found}"
      return 0
    fi
    if [[ "${elapsed}" -eq 0 ]]; then
      echo "[phase60] waiting for authoritative ingress front-door Service" >&2
    fi
    sleep "${delay}"
    elapsed=$((elapsed + delay))
  done

  return 1
}

argocd_debug_dump() {
  local ts="" host=""
  ts="$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date)"
  host="${ARGOCD_DEBUG_HOST:-argocd.${CLUSTER_LOCAL_DOMAIN}}"

  if [[ "${PHASE60_ARGOCD_DEBUG}" != "1" && "${PHASE60_ARGOCD_DEBUG}" != "true" ]]; then
    return 0
  fi

  mkdir -p "$(dirname "${PHASE60_ARGOCD_DEBUG_LOG}")" 2>/dev/null || true
  {
    echo ""
    echo "===== phase60 argocd debug @ ${ts} ====="
    echo "[env] host=${host} kubeconfig=${KUBECONFIG:-unset}"
    echo "[dns]"
    if command -v getent >/dev/null 2>&1; then
      getent hosts "${host}" 2>/dev/null || true
    elif command -v host >/dev/null 2>&1; then
      host "${host}" 2>/dev/null || true
    else
      echo "dns tool not available"
    fi
    echo "[kubectl] argocd namespace"
    "${kubectl_bin}" get ns argocd -o name 2>/dev/null || true
    echo "[kubectl] argocd-cmd-params-cm"
    "${kubectl_bin}" -n argocd get cm argocd-cmd-params-cm -o yaml 2>/dev/null || true
    echo "[kubectl] argocd pods"
    "${kubectl_bin}" -n argocd get pods -o wide 2>/dev/null || true
    echo "[kubectl] argocd-server deployment args"
    "${kubectl_bin}" -n argocd get deploy argocd-server \
      -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null || true
    echo
    echo "[kubectl] argocd-repo-server deployment"
    "${kubectl_bin}" -n argocd get deploy argocd-repo-server -o yaml 2>/dev/null || true
    echo "[kubectl] argocd-repo-server service/endpoints"
    "${kubectl_bin}" -n argocd get svc argocd-repo-server -o yaml 2>/dev/null || true
    "${kubectl_bin}" -n argocd get endpoints argocd-repo-server -o yaml 2>/dev/null || true
    echo "[kubectl] argocd-repo-server recent logs"
    "${kubectl_bin}" -n argocd logs deploy/argocd-repo-server --tail=200 2>/dev/null || true
    echo "[kubectl] argocd ingress/service"
    "${kubectl_bin}" -n argocd get ingress argocd-ui -o yaml 2>/dev/null || true
    "${kubectl_bin}" -n argocd get svc argocd-server -o yaml 2>/dev/null || true
    echo "[kubectl] ingress controllers"
    "${kubectl_bin}" -n kube-system get svc rke2-ingress-nginx-controller -o yaml 2>/dev/null || true
    "${kubectl_bin}" -n ingress-nginx get svc ingress-nginx-controller -o yaml 2>/dev/null || true
    "${kubectl_bin}" get svc -A \
      -l app.kubernetes.io/name=ingress-nginx,app.kubernetes.io/component=controller \
      -o yaml 2>/dev/null || true
    echo "[kubectl] ingress controller workloads"
    "${kubectl_bin}" get deploy,ds,pod -A \
      -l app.kubernetes.io/name=ingress-nginx \
      -o wide 2>/dev/null || true
    echo "[kubectl] helm-managed ingress objects"
    "${kubectl_bin}" -n kube-system get helmchart,helmchartconfig 2>/dev/null || true
    "${kubectl_bin}" -n kube-system get helmchart rke2-ingress-nginx -o yaml 2>/dev/null || true
    "${kubectl_bin}" -n kube-system get helmchartconfig rke2-ingress-nginx -o yaml 2>/dev/null || true
    echo "[kubectl] ingress-nginx events"
    "${kubectl_bin}" -n kube-system get events --sort-by=.lastTimestamp 2>/dev/null | grep -i 'ingress\|nginx\|helm-install' || true
    echo "[curl] http trace"
    curl -sS -I --connect-timeout 5 --max-redirs 10 "http://${host}/" 2>&1 || true
    echo "[curl] https trace"
    curl -k -sS -I --connect-timeout 5 --max-redirs 10 "https://${host}/" 2>&1 || true
    echo "===== end phase60 argocd debug ====="
  } >>"${PHASE60_ARGOCD_DEBUG_LOG}" 2>&1
  bootstrap_diag_record_file_event \
    "argocd" \
    "argocd-debug" \
    "captured argocd diagnostics" \
    "${PHASE60_ARGOCD_DEBUG_LOG}" \
    "warning" \
    "http"
}

openbao_token=""
if "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token >/dev/null 2>&1; then
  openbao_token="$(
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" get secret openbao-bootstrap-token \
      -o jsonpath='{.data.token}' 2>/dev/null | base64 -d
  )"
fi

github_app_refresh_repo_auth

if [[ "${PHASE60_MODE}" != "realize" ]]; then
argocd_admin_password="${ARGOCD_ADMIN_PASSWORD:-}"
if [[ -z "${argocd_admin_password}" && -n "${BOOTSTRAP_SECRET_DIR:-}" ]]; then
  argocd_admin_password="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_admin_password")"
fi

preferred_seed_repo_url="${GITEA_SEED_SOURCE_REPO_URL:-${ARGOCD_GITHUB_REPO_URL:-}}"
preferred_seed_repo_branch="${GITEA_SEED_SOURCE_REPO_BRANCH:-${ARGOCD_GITHUB_REPO_BRANCH:-HEAD}}"
preferred_seed_repo_token="${GITEA_SEED_SOURCE_TOKEN:-${ARGOCD_GITHUB_TOKEN:-}}"
bootstrap_seed_repo_url="${preferred_seed_repo_url:-${ARGOCD_REPO_URL:-}}"
bootstrap_seed_repo_branch="${preferred_seed_repo_branch:-${ARGOCD_REPO_BRANCH:-HEAD}}"
bootstrap_seed_repo_token="${preferred_seed_repo_token:-}"

gitea_repo_username_effective="$(read_k8s_secret_key gitea gitea-admin-secret username)"
gitea_repo_password_effective="$(read_k8s_secret_key gitea gitea-admin-secret password)"
if [[ -z "${gitea_repo_username_effective}" ]]; then
  gitea_repo_username_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_admin_username")"
fi
if [[ -z "${gitea_repo_username_effective}" ]]; then
  gitea_repo_username_effective="gitea-admin"
fi
if [[ -z "${gitea_repo_password_effective}" ]]; then
  gitea_repo_password_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_admin_password")"
fi
if [[ -n "${gitea_repo_username_effective}" ]]; then
  write_secret_file gitea_admin_username "${gitea_repo_username_effective}"
fi
if [[ -n "${gitea_repo_password_effective}" ]]; then
  write_secret_file gitea_admin_password "${gitea_repo_password_effective}"
fi
gitea_git_token_effective_value="$(gitea_git_token_effective || true)"
if [[ -n "${gitea_git_token_effective_value}" ]]; then
  write_secret_file gitea_git_token "${gitea_git_token_effective_value}"
fi
if [[ -n "${CLOUDFLARE_ACCOUNT_ID:-}" ]]; then
  write_secret_file cloudflare_account_id "${CLOUDFLARE_ACCOUNT_ID}"
fi
if [[ -n "${CLOUDFLARE_API_TOKEN:-}" ]]; then
  write_secret_file cloudflare_api_token "${CLOUDFLARE_API_TOKEN}"
fi
if [[ -n "${RANCHER_CLOUDFLARED_TUNNEL_ID:-}" ]]; then
  write_secret_file rancher_cloudflared_tunnel_id "${RANCHER_CLOUDFLARED_TUNNEL_ID}"
fi
authentik_secret_key_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_secret_key")"
authentik_postgresql_password_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_postgresql_password")"
authentik_admin_username_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_admin_username")"
authentik_admin_password_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_admin_password")"
authentik_bootstrap_token_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/authentik_bootstrap_token")"
headlamp_admin_token_effective=""
authentik_admin_source="local-backup"

if [[ -z "${authentik_admin_username_effective:-}" ]]; then
  authentik_admin_username_effective="akadmin"
fi
if [[ -z "${authentik_admin_password_effective:-}" ]]; then
  authentik_admin_password_effective="$(generate_random_secret_value 24 || true)"
  if [[ -z "${authentik_admin_password_effective:-}" ]]; then
    fail_local_requirement "failed generating local Authentik admin password"
  fi
  authentik_admin_source="local-generated"
fi
write_secret_file authentik_admin_username "${authentik_admin_username_effective}"
write_secret_file authentik_admin_password "${authentik_admin_password_effective}"
echo "[phase60] Authentik admin backup credentials are authoritative from ${BOOTSTRAP_SECRET_DIR} (source=${authentik_admin_source})"

if [[ -n "${BOOTSTRAP_SECRET_DIR:-}" ]]; then
  if [[ -z "${ARGOCD_REPO_URL:-}" ]]; then
    seed_repo_url="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_url")"
    if [[ -n "${seed_repo_url}" ]]; then
      export ARGOCD_REPO_URL="${seed_repo_url}"
    fi
  fi
  if [[ -z "${ARGOCD_REPO_USERNAME:-}" ]]; then
    seed_repo_user="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_username")"
    if [[ -n "${seed_repo_user}" ]]; then
      export ARGOCD_REPO_USERNAME="${seed_repo_user}"
    fi
  fi
  if [[ -z "${ARGOCD_REPO_TOKEN:-}" ]]; then
    seed_repo_token="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_token")"
    if [[ -n "${seed_repo_token}" ]]; then
      export ARGOCD_REPO_TOKEN="${seed_repo_token}"
    fi
  fi
  if [[ -z "${ARGOCD_REPO_BRANCH:-}" ]]; then
    seed_repo_branch="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_branch")"
    if [[ -n "${seed_repo_branch}" ]]; then
      export ARGOCD_REPO_BRANCH="${seed_repo_branch}"
    fi
  fi
  if [[ -n "${ARGOCD_REPO_URL:-}" ]] && printf '%s' "${ARGOCD_REPO_URL}" | grep -q '/gitea-admin/cluster\.git'; then
    export ARGOCD_REPO_URL="$(normalize_gitea_repo_url "${ARGOCD_REPO_URL}")"
    if [[ -n "${gitea_repo_username_effective}" ]]; then
      export ARGOCD_REPO_USERNAME="${gitea_repo_username_effective}"
    fi
    if [[ -n "${gitea_git_token_effective_value:-}" ]]; then
      export ARGOCD_REPO_TOKEN="${gitea_git_token_effective_value}"
    fi
  fi
fi

if [[ -n "${authentik_secret_key_effective:-}" || -n "${authentik_admin_password_effective:-}" || -n "${authentik_bootstrap_token_effective:-}" ]]; then
  ensure_authentik_secret \
    "${authentik_secret_key_effective}" \
    "${authentik_postgresql_password_effective}" \
    "${authentik_admin_password_effective}" \
    "${authentik_bootstrap_token_effective}" || true
fi

ensure_homepage_widget_secret || true
headlamp_admin_token_effective="$(ensure_headlamp_admin_token || true)"

if [[ -n "${authentik_admin_username_effective:-}" ]]; then
  write_secret_file authentik_admin_username "${authentik_admin_username_effective}"
fi
if [[ -n "${authentik_admin_password_effective:-}" ]]; then
  write_secret_file authentik_admin_password "${authentik_admin_password_effective}"
fi
if [[ -n "${authentik_bootstrap_token_effective:-}" ]]; then
  write_secret_file authentik_bootstrap_token "${authentik_bootstrap_token_effective}"
fi

if [[ -n "${openbao_token}" ]]; then
  if [[ -z "${authentik_admin_username_effective:-}" ]]; then
    authentik_admin_username_effective="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=authentik_admin_username secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${authentik_admin_password_effective:-}" ]]; then
    authentik_admin_password_effective="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=authentik_admin_password secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${authentik_bootstrap_token_effective:-}" ]]; then
    authentik_bootstrap_token_effective="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=authentik_bootstrap_token secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${authentik_secret_key_effective:-}" ]]; then
    authentik_secret_key_effective="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=authentik_secret_key secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -z "${authentik_postgresql_password_effective:-}" ]]; then
    authentik_postgresql_password_effective="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -field=authentik_postgresql_password secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -n "${authentik_admin_username_effective:-}" || -n "${authentik_admin_password_effective:-}" || -n "${authentik_bootstrap_token_effective:-}" ]]; then
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv patch secret/bootstrap/platform \
        "authentik_admin_username=${authentik_admin_username_effective}" \
        "authentik_admin_password=${authentik_admin_password_effective}" \
        "authentik_bootstrap_token=${authentik_bootstrap_token_effective}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${headlamp_admin_token_effective}" ]]; then
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv patch secret/bootstrap/platform \
        "headlamp_admin_username=${headlamp_admin_username:-headlamp}" \
        "headlamp_admin_token=${headlamp_admin_token_effective}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${authentik_admin_username_effective:-}" ]]; then
    write_secret_file authentik_admin_username "${authentik_admin_username_effective}"
  fi
  if [[ -n "${authentik_admin_password_effective:-}" ]]; then
    write_secret_file authentik_admin_password "${authentik_admin_password_effective}"
  fi
  if [[ -n "${authentik_bootstrap_token_effective:-}" ]]; then
    write_secret_file authentik_bootstrap_token "${authentik_bootstrap_token_effective}"
  fi
  if [[ -n "${authentik_secret_key_effective:-}" ]]; then
    write_secret_file authentik_secret_key "${authentik_secret_key_effective}"
  fi
  if [[ -n "${authentik_postgresql_password_effective:-}" ]]; then
    write_secret_file authentik_postgresql_password "${authentik_postgresql_password_effective}"
  fi
  if [[ -n "${authentik_secret_key_effective:-}" || -n "${authentik_admin_password_effective:-}" || -n "${authentik_bootstrap_token_effective:-}" ]]; then
    ensure_authentik_secret \
      "${authentik_secret_key_effective}" \
      "${authentik_postgresql_password_effective}" \
      "${authentik_admin_password_effective}" \
      "${authentik_bootstrap_token_effective}" || true
  fi
  argocd_json="$(
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv get -format=json secret/bootstrap/argocd 2>/dev/null || true
  )"
  if [[ -n "${argocd_json}" ]]; then
    if [[ -z "${argocd_admin_password}" ]]; then
      argocd_admin_password="$(
        printf '%s' "${argocd_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("admin_password",""))
except Exception:
  print("")
PY
      )"
    fi
    if [[ -z "${ARGOCD_REPO_URL:-}" ]]; then
      export ARGOCD_REPO_URL="$(
        printf '%s' "${argocd_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("repo_url",""))
except Exception:
  print("")
PY
      )"
    fi
    if [[ -z "${ARGOCD_REPO_USERNAME:-}" ]]; then
      export ARGOCD_REPO_USERNAME="$(
        printf '%s' "${argocd_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("repo_username",""))
except Exception:
  print("")
PY
      )"
    fi
    if [[ -z "${ARGOCD_REPO_TOKEN:-}" ]]; then
      export ARGOCD_REPO_TOKEN="$(
        printf '%s' "${argocd_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("repo_token",""))
except Exception:
  print("")
PY
      )"
    fi
    if [[ -z "${ARGOCD_REPO_BRANCH:-}" ]]; then
      export ARGOCD_REPO_BRANCH="$(
        printf '%s' "${argocd_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("repo_branch",""))
except Exception:
  print("")
PY
      )"
    fi
  fi
fi

if [[ -n "${openbao_token}" && ( -z "${CLOUDFLARE_ZONE_API_TOKEN:-}" || -z "${CLOUDFLARE_API_TOKEN:-}" ) ]]; then
  platform_json="$(
    "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv get -format=json secret/bootstrap/platform 2>/dev/null || true
  )"
  if [[ -n "${platform_json}" ]]; then
    if [[ -z "${CLOUDFLARE_ZONE_API_TOKEN:-}" ]]; then
      export CLOUDFLARE_ZONE_API_TOKEN="$(
        printf '%s' "${platform_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("cloudflare_zone_api_token",""))
except Exception:
  print("")
PY
      )"
    fi
    if [[ -z "${CLOUDFLARE_API_TOKEN:-}" ]]; then
      export CLOUDFLARE_API_TOKEN="$(
        printf '%s' "${platform_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("cloudflare_api_token",""))
except Exception:
  print("")
PY
      )"
    fi
  fi
fi

if [[ -n "${openbao_token}" && ( -z "${authentik_admin_username_effective:-}" || -z "${authentik_admin_password_effective:-}" || -z "${authentik_bootstrap_token_effective:-}" ) ]]; then
  if [[ -z "${platform_json:-}" ]]; then
    platform_json="$(
      "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
        env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
        bao kv get -format=json secret/bootstrap/platform 2>/dev/null || true
    )"
  fi
  if [[ -n "${platform_json}" ]]; then
    if [[ -z "${authentik_admin_username_effective:-}" ]]; then
      authentik_admin_username_effective="$(
        printf '%s' "${platform_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("authentik_admin_username",""))
except Exception:
  print("")
PY
      )"
    fi
    if [[ -z "${authentik_admin_password_effective:-}" ]]; then
      authentik_admin_password_effective="$(
        printf '%s' "${platform_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("authentik_admin_password",""))
except Exception:
  print("")
PY
      )"
      if [[ -n "${authentik_admin_password_effective:-}" ]]; then
        authentik_admin_source="openbao"
        echo "[phase60] Authentik admin backup credentials restored from OpenBao platform secret"
      fi
    fi
    if [[ -z "${authentik_bootstrap_token_effective:-}" ]]; then
      authentik_bootstrap_token_effective="$(
        printf '%s' "${platform_json}" | python3 - <<'PY'
import json,sys
try:
  data=json.load(sys.stdin).get("data",{}).get("data",{})
  print(data.get("authentik_bootstrap_token",""))
except Exception:
  print("")
PY
      )"
    fi
  fi
fi

if [[ -n "${authentik_admin_username_effective:-}" ]]; then
  write_secret_file authentik_admin_username "${authentik_admin_username_effective}"
fi
if [[ -n "${authentik_admin_password_effective:-}" ]]; then
  write_secret_file authentik_admin_password "${authentik_admin_password_effective}"
fi
if [[ -n "${authentik_bootstrap_token_effective:-}" ]]; then
  write_secret_file authentik_bootstrap_token "${authentik_bootstrap_token_effective}"
fi

if [[ -z "${bootstrap_seed_repo_url}" ]]; then
  bootstrap_seed_repo_url="${preferred_seed_repo_url:-${ARGOCD_REPO_URL:-}}"
fi
if [[ -z "${bootstrap_seed_repo_branch}" ]]; then
  bootstrap_seed_repo_branch="${preferred_seed_repo_branch:-${ARGOCD_REPO_BRANCH:-HEAD}}"
fi
if [[ -z "${bootstrap_seed_repo_token}" ]]; then
  bootstrap_seed_repo_token="${preferred_seed_repo_token:-}"
fi
if [[ -n "${ARGOCD_REPO_URL:-}" ]] && printf '%s' "${ARGOCD_REPO_URL}" | grep -q '/gitea-admin/cluster\.git'; then
  export ARGOCD_REPO_URL="$(normalize_gitea_repo_url "${ARGOCD_REPO_URL}")"
  if [[ -n "${gitea_repo_username_effective}" ]]; then
    export ARGOCD_REPO_USERNAME="${gitea_repo_username_effective}"
  fi
  if [[ -n "${gitea_git_token_effective_value:-}" ]]; then
    export ARGOCD_REPO_TOKEN="${gitea_git_token_effective_value}"
  fi
fi

if [[ -n "${ARGOCD_REPO_URL:-}" ]] && printf '%s' "${ARGOCD_REPO_URL}" | grep -q '/gitea-admin/cluster\.git'; then
  if [[ -n "${preferred_seed_repo_url:-}" ]]; then
    bootstrap_seed_repo_url="${preferred_seed_repo_url}"
    bootstrap_seed_repo_branch="${preferred_seed_repo_branch:-${bootstrap_seed_repo_branch:-HEAD}}"
    bootstrap_seed_repo_token="${preferred_seed_repo_token:-${bootstrap_seed_repo_token}}"
  elif [[ -n "${ARGOCD_REPO_TOKEN:-}" ]]; then
    echo "[phase60] Argo CD already points at the Gitea bootstrap repo; skipping GitHub source reseed"
    bootstrap_seed_repo_url="${ARGOCD_REPO_URL}"
    bootstrap_seed_repo_branch="${ARGOCD_REPO_BRANCH:-${bootstrap_seed_repo_branch:-HEAD}}"
    bootstrap_seed_repo_token="${ARGOCD_REPO_TOKEN}"
    unset GITEA_SEED_SOURCE_REPO_URL || true
  else
    bootstrap_seed_repo_url=""
  fi
elif [[ -n "${bootstrap_seed_repo_url}" ]] && printf '%s' "${bootstrap_seed_repo_url}" | grep -q '/gitea-admin/cluster\.git'; then
  if [[ -n "${preferred_seed_repo_url:-}" ]]; then
    bootstrap_seed_repo_url="${preferred_seed_repo_url}"
    bootstrap_seed_repo_branch="${preferred_seed_repo_branch:-${bootstrap_seed_repo_branch}}"
    bootstrap_seed_repo_token="${preferred_seed_repo_token:-${bootstrap_seed_repo_token}}"
  else
    bootstrap_seed_repo_url=""
  fi
fi

if [[ -n "${bootstrap_seed_repo_url:-}" ]] && printf '%s' "${bootstrap_seed_repo_url}" | grep -qE '^https://github.com/'; then
  bootstrap_seed_repo_token="${preferred_seed_repo_token:-}"
fi

if [[ -n "${argocd_admin_password}" ]]; then
  export ARGOCD_ADMIN_PASSWORD="${argocd_admin_password}"
else
  echo "[phase60] ARGOCD_ADMIN_PASSWORD missing; Argo CD will use chart defaults." >&2
fi

if [[ "${PHASE60_RECONCILE_ONLY}" == "1" || "${PHASE60_RECONCILE_ONLY}" == "true" ]]; then
  echo "[phase60] reconcile-only mode: skipping Argo CD install playbook"
else
  echo "[phase60] installing Argo CD"
  ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}" -e break_glass=true
fi

if [[ "${PHASE60_RECONCILE_ONLY}" == "1" || "${PHASE60_RECONCILE_ONLY}" == "true" ]]; then
  echo "[phase60] reconcile-only mode: skipping Gitea bootstrap and repo handoff"
else
  if [[ -z "${gitea_repo_password_effective}" ]]; then
    fail_local_requirement "Gitea admin password missing; cannot bootstrap Gitea through Argo"
  fi
  if [[ -z "${bootstrap_seed_repo_url}" ]]; then
    fail_local_requirement "bootstrap source repo URL missing; set GITEA_SEED_SOURCE_REPO_URL or ARGOCD_REPO_URL"
  fi

  echo "[phase60] bootstrapping Gitea through Argo CD before repo handoff"
  ensure_gitea_admin_secret "${gitea_repo_password_effective}"
  apply_bootstrap_gitea_application
  if ! wait_for_gitea_application 90 5; then
    fail_local_requirement "Argo CD did not create deployment/gitea in namespace gitea within the expected time"
  fi
  require_gitea_golden_path
  refresh_gitea_internal_service_url
  run_or_fail \
    "failed configuring runtime host aliases for Gitea registry auth" \
    ensure_gitea_runtime_host_aliases
  run_or_fail \
    "failed configuring rke2 registry runtime for ansible-runner pulls" \
    ensure_ansible_runner_registry_runtime
  gitea_runner_token_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_runner_token")"
  run_or_fail \
    "failed ensuring Gitea actions runner deployment" \
    ensure_gitea_actions_runner \
    "${gitea_runner_token_effective}" \
    "${GITEA_INTERNAL_URL}" \
    "${BOOTSTRAP_HOSTNAME:-cluster}-runner"
  trust_existing_gitea="${GITEA_SEED_TRUST_EXISTING_GITEA:-0}"
  case "${trust_existing_gitea}" in
    0|false|FALSE|no|NO|off|OFF)
      run_or_fail \
        "failed seeding Gitea bootstrap repo from source" \
        seed_gitea_bootstrap_repo \
        "${gitea_repo_password_effective}" \
        "${GITEA_CANONICAL_URL}" \
        "${bootstrap_seed_repo_url}" \
        "${bootstrap_seed_repo_branch}" \
        "${bootstrap_seed_repo_token}" \
        "${GITEA_SEED_TARGET_OWNER:-gitea-admin}" \
        "${GITEA_SEED_TARGET_REPO:-cluster}"
      ;;
    *)
      gitea_target_owner="${GITEA_SEED_TARGET_OWNER:-gitea-admin}"
      gitea_target_repo="${GITEA_SEED_TARGET_REPO:-cluster}"
      gitea_repo_url_direct="${GITEA_CANONICAL_URL}${gitea_target_owner}/${gitea_target_repo}.git"
      argocd_repo_url_direct="$(argocd_gitea_repo_url "${gitea_target_owner}" "${gitea_target_repo}")"
      if gitea_repo_has_readable_refs \
        "${argocd_repo_url_direct}" \
        "${gitea_repo_username_effective}" \
        "${gitea_git_token_effective_value:-${gitea_repo_password_effective}}"; then
        write_secret_file argocd_repo_url "${argocd_repo_url_direct}"
        write_secret_file argocd_repo_username "${gitea_repo_username_effective}"
        write_secret_file argocd_repo_token "${gitea_git_token_effective_value:-${gitea_repo_password_effective}}"
        write_secret_file argocd_repo_branch "${bootstrap_seed_repo_branch:-HEAD}"
        echo "[phase60] trusting existing Gitea bootstrap repo with refs; skipping seed helper and remote reseed"
        echo "[phase60] Gitea repo selected -> ${gitea_repo_url_direct}"
        echo "[phase60] Argo CD repo source -> ${argocd_repo_url_direct}"
      else
        echo "[phase60] existing Gitea bootstrap repo has no refs; seeding from source repo"
        run_or_fail \
          "failed seeding Gitea bootstrap repo from source" \
          seed_gitea_bootstrap_repo \
          "${gitea_repo_password_effective}" \
          "${GITEA_CANONICAL_URL}" \
          "${bootstrap_seed_repo_url}" \
          "${bootstrap_seed_repo_branch}" \
          "${bootstrap_seed_repo_token}" \
          "${gitea_target_owner}" \
          "${gitea_target_repo}"
      fi
      ;;
  esac

  gitea_push_mirror_enabled_effective="${GITEA_PUSH_MIRROR_ENABLED:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_push_mirror_enabled")}"
  gitea_push_mirror_repo_url_effective="${GITEA_PUSH_MIRROR_REPO_URL:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_push_mirror_repo_url")}"
  gitea_push_mirror_username_effective="${GITEA_PUSH_MIRROR_USERNAME:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_push_mirror_username")}"
  gitea_push_mirror_token_effective="${GITEA_PUSH_MIRROR_TOKEN:-$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/gitea_push_mirror_token")}"
  if [[ -z "${gitea_push_mirror_repo_url_effective}" ]] && [[ -n "${preferred_seed_repo_url:-}" ]] && repo_url_is_github "${preferred_seed_repo_url}"; then
    gitea_push_mirror_repo_url_effective="${preferred_seed_repo_url}"
  fi
  if [[ -z "${gitea_push_mirror_username_effective}" ]]; then
    gitea_push_mirror_username_effective="${GITEA_SEED_SOURCE_USERNAME:-${ARGOCD_GITHUB_USERNAME:-}}"
  fi
  if [[ -z "${gitea_push_mirror_token_effective}" ]]; then
    gitea_push_mirror_token_effective="${GITEA_SEED_SOURCE_TOKEN:-${ARGOCD_GITHUB_TOKEN:-${GITHUB_SYNC_TOKEN:-}}}"
  fi
  if [[ -z "${gitea_push_mirror_enabled_effective}" ]]; then
    if [[ -n "${gitea_push_mirror_repo_url_effective}" ]] && [[ -n "${gitea_push_mirror_token_effective}" ]]; then
      gitea_push_mirror_enabled_effective="1"
    else
      gitea_push_mirror_enabled_effective="0"
    fi
  fi
  if [[ "${gitea_push_mirror_enabled_effective}" == "1" || "${gitea_push_mirror_enabled_effective}" == "true" || "${gitea_push_mirror_enabled_effective}" == "yes" ]]; then
    if [[ -n "${gitea_push_mirror_repo_url_effective}" ]] && [[ -n "${gitea_push_mirror_username_effective}" ]] && [[ -n "${gitea_push_mirror_token_effective}" ]]; then
      write_secret_file gitea_push_mirror_enabled "1"
      write_secret_file gitea_push_mirror_repo_url "${gitea_push_mirror_repo_url_effective}"
      write_secret_file gitea_push_mirror_username "${gitea_push_mirror_username_effective}"
      write_secret_file gitea_push_mirror_token "${gitea_push_mirror_token_effective}"
      if [[ -n "${openbao_token}" ]]; then
        retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
          env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
          bao kv patch secret/bootstrap/platform \
            "gitea_push_mirror_enabled=1" \
            "gitea_push_mirror_repo_url=${gitea_push_mirror_repo_url_effective}" \
            "gitea_push_mirror_username=${gitea_push_mirror_username_effective}" \
            "gitea_push_mirror_token=${gitea_push_mirror_token_effective}" >/dev/null 2>&1 || true
      fi
      if ! configure_gitea_push_mirror \
        "${GITEA_SEED_TARGET_OWNER:-gitea-admin}" \
        "${GITEA_SEED_TARGET_REPO:-cluster}" \
        "${gitea_push_mirror_repo_url_effective}" \
        "${gitea_push_mirror_username_effective}" \
        "${gitea_push_mirror_token_effective}"; then
        echo "[phase60] WARNING: failed configuring Gitea push mirror to GitHub; continuing bootstrap" >&2
      fi
    else
      echo "[phase60] Gitea push mirror requested but repo URL, username, or token is missing; skipping" >&2
    fi
  else
    echo "[phase60] Gitea push mirror disabled"
  fi

  argocd_repo_url_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_url")"
  argocd_repo_username_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_username")"
  argocd_repo_token_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_token")"
  argocd_repo_branch_effective="$(read_secret_file "${BOOTSTRAP_SECRET_DIR}/argocd_repo_branch")"

  if [[ -z "${argocd_repo_url_effective}" ]]; then
    fail_local_requirement "Gitea seed completed without writing argocd_repo_url"
  fi
fi
if [[ "${PHASE60_RECONCILE_ONLY}" == "1" || "${PHASE60_RECONCILE_ONLY}" == "true" ]]; then
  echo "[phase60] reconcile-only mode: skipping Argo CD repo handoff reconciliation"
else
  if [[ -z "${argocd_repo_branch_effective}" ]]; then
    argocd_repo_branch_effective="HEAD"
  fi
  if [[ -n "${argocd_repo_token_effective}" && -z "${argocd_repo_username_effective}" ]]; then
    argocd_repo_username_effective="gitea-admin"
  fi

  if [[ -n "${openbao_token}" ]]; then
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv put secret/bootstrap/argocd \
        "admin_password=${ARGOCD_ADMIN_PASSWORD:-}" \
        "repo_url=${argocd_repo_url_effective}" \
        "repo_username=${argocd_repo_username_effective}" \
        "repo_token=${argocd_repo_token_effective}" \
        "repo_branch=${argocd_repo_branch_effective}" >/dev/null 2>&1 || true
  fi

  reconcile_argocd_bootstrap_repo \
    "${argocd_repo_url_effective}" \
    "${argocd_repo_username_effective}" \
    "${argocd_repo_token_effective}" \
    "${argocd_repo_branch_effective}" \
    "pods/argocd"
  reconcile_pre_openbao_application_repo \
    "${argocd_repo_url_effective}" \
    "${argocd_repo_branch_effective}" \
    "pods/argocd/platform/pre-openbao"
  verify_gitea_gitops_handoff \
    "${argocd_repo_url_effective}" \
    "${argocd_repo_username_effective}" \
    "${argocd_repo_token_effective}" \
    "${argocd_repo_branch_effective}" \
    "pods/argocd/platform/pre-openbao"
fi

echo "[phase60] enforcing Argo CD proxy mode (server.insecure=true)"
if "${kubectl_bin}" -n argocd get cm argocd-cmd-params-cm >/dev/null 2>&1; then
  "${kubectl_bin}" -n argocd patch cm argocd-cmd-params-cm --type=merge \
    -p '{"data":{"server.insecure":"true"}}'
else
  "${kubectl_bin}" -n argocd create cm argocd-cmd-params-cm \
    --from-literal=server.insecure=true >/dev/null 2>&1 || true
fi

if "${kubectl_bin}" -n argocd get deploy argocd-server >/dev/null 2>&1; then
  "${kubectl_bin}" -n argocd rollout restart deploy argocd-server >/dev/null 2>&1 || true
  "${kubectl_bin}" -n argocd rollout status deploy argocd-server --timeout=300s >/dev/null 2>&1 || true
fi
argocd_debug_dump

echo "[phase60] GitOps handoff complete: Argo CD now points at the seeded Gitea repo"
if [[ "${PHASE60_MODE}" == "handoff" ]]; then
  if [[ -d "${repo_root}/pods/ingress" ]]; then
    echo "[phase60] ensuring ingress front-door VIP before handoff exit"
    run_or_fail \
      "failed ensuring ingress front-door VIP before burn-the-ladder" \
      ensure_routing_frontdoor_vip
  fi
  echo "[phase60] realization checks deferred to the non-blocking realization validation path"
  echo "[phase60] complete (GitOps handoff)"
  exit 0
fi
fi

echo "[phase60] running realization checks (warning-only=${PHASE60_WARNING_ONLY})"

gitea_local_host=""
argocd_local_host=""
gitea_external_host=""
argocd_external_host=""
registry_external_host=""
homepage_external_host=""
authentik_external_host=""
headlamp_external_host=""
alertmanager_external_host=""
grafana_external_host=""
prometheus_external_host=""
local_domain_expected_vip=""

# Ensure the namespace exists when applying kustomize directly (Argo would create it,
# but this path keeps routing online immediately).
if ! "${kubectl_bin}" create namespace ingress --dry-run=client -o yaml | "${kubectl_bin}" apply -f - >/dev/null; then
  fail_local_requirement "failed to ensure ingress namespace exists"
fi

if [[ -d "${repo_root}/pods/ingress" ]]; then
  gitea_external_host="gitea.${CLUSTER_DOMAIN}"
  argocd_external_host="argocd.${CLUSTER_DOMAIN}"
  registry_external_host="registry.${CLUSTER_DOMAIN}"
  homepage_external_host="home.${CLUSTER_DOMAIN}"
  gitea_local_host="gitea.${CLUSTER_LOCAL_DOMAIN}"
  argocd_local_host="argocd.${CLUSTER_LOCAL_DOMAIN}"
  authentik_external_host="authentik.${CLUSTER_DOMAIN}"
  authentik_local_host="authentik.${CLUSTER_LOCAL_DOMAIN}"
  headlamp_external_host="headlamp.${CLUSTER_DOMAIN}"
  alertmanager_external_host="alertmanager.${CLUSTER_DOMAIN}"
  grafana_external_host="grafana.${CLUSTER_DOMAIN}"
  prometheus_external_host="prometheus.${CLUSTER_DOMAIN}"

  external_dns_bootstrap_hosts="${gitea_external_host},${argocd_external_host},${registry_external_host},${authentik_external_host},${headlamp_external_host},${homepage_external_host},${alertmanager_external_host},${grafana_external_host},${prometheus_external_host}"

  echo "[phase60] ingress hostnames: external=${external_dns_bootstrap_hosts} local=${gitea_local_host},${argocd_local_host}"
  echo "[phase60] applying early-safe ingress resources"
  run_or_fail \
    "failed applying early-safe ingress resources" \
    "${kubectl_bin}" apply -f "${repo_root}/pods/ingress/ingress-cluster-config.yaml" \
      -f "${repo_root}/pods/ingress/external-dns/rbac.yaml" \
      -f "${repo_root}/pods/ingress/external-dns/deployment.yaml" \
      -f "${repo_root}/pods/ingress/nginx-routing/gitea-ingress.yaml" \
      -f "${repo_root}/pods/ingress/nginx-routing/argocd-ingress.yaml" \
      -f "${repo_root}/pods/ingress/nginx-routing/registry-ingress.yaml"
  for optional_ns in authentik headlamp homepage; do
    if "${kubectl_bin}" get namespace "${optional_ns}" >/dev/null 2>&1; then
      echo "[phase60] applying optional ingress for namespace ${optional_ns}"
      case "${optional_ns}" in
        authentik)
          run_or_fail \
            "failed applying pods/ingress/nginx-routing/authentik-ingress.yaml" \
            "${kubectl_bin}" apply -f "${repo_root}/pods/ingress/nginx-routing/authentik-ingress.yaml"
          ;;
        headlamp)
          run_or_fail \
            "failed applying pods/ingress/nginx-routing/headlamp-ingress.yaml" \
            "${kubectl_bin}" apply -f "${repo_root}/pods/ingress/nginx-routing/headlamp-ingress.yaml" \
              -f "${repo_root}/pods/ingress/nginx-routing/headlamp-public-ingress.yaml"
          ;;
        homepage)
          run_or_fail \
            "failed applying pods/ingress/nginx-routing/homepage-ingress.yaml" \
            "${kubectl_bin}" apply -f "${repo_root}/pods/ingress/nginx-routing/homepage-ingress.yaml" \
              -f "${repo_root}/pods/ingress/nginx-routing/homepage-public-ingress.yaml"
          ;;
      esac
    else
      echo "[phase60] namespace ${optional_ns} not present yet; skipping optional ingress"
    fi
  done
  if [[ -d "${repo_root}/pods/ingress/observability-routing" ]]; then
    if "${kubectl_bin}" get namespace observability >/dev/null 2>&1; then
      echo "[phase60] applying pods/ingress/observability-routing"
      run_or_fail \
        "failed applying pods/ingress/observability-routing" \
        "${kubectl_bin}" apply -k "${repo_root}/pods/ingress/observability-routing"
    else
      echo "[phase60] observability namespace not present yet; skipping pods/ingress/observability-routing"
    fi
  fi
  # Normalize ingress hosts from env so we can keep one source of truth:
  # CLUSTER_DOMAIN is primary; CLUSTER_LOCAL_DOMAIN is optional override for LAN/VPN DNS.
  cat <<EOF | "${kubectl_bin}" apply -f - >/dev/null 2>&1 || true
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: gitea-ui
  namespace: gitea
spec:
  ingressClassName: nginx
  rules:
    - host: ${gitea_local_host}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ${GITEA_INTERNAL_SERVICE_NAME}
                port:
                  number: 3000
    - host: ${gitea_external_host}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ${GITEA_INTERNAL_SERVICE_NAME}
                port:
                  number: 3000
EOF

  cat <<EOF | "${kubectl_bin}" apply -f - >/dev/null 2>&1 || true
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: argocd-ui
  namespace: argocd
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "HTTP"
    nginx.ingress.kubernetes.io/ssl-redirect: "false"
    nginx.ingress.kubernetes.io/force-ssl-redirect: "false"
spec:
  ingressClassName: nginx
  rules:
    - host: ${argocd_local_host}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: argocd-server
                port:
                  number: 80
    - host: ${argocd_external_host}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: argocd-server
                port:
                  number: 80
EOF

  run_or_fail \
    "failed annotating argocd ingress (argocd/argocd-ui)" \
    "${kubectl_bin}" -n argocd annotate ingress argocd-ui \
      nginx.ingress.kubernetes.io/backend-protocol=HTTP \
      nginx.ingress.kubernetes.io/ssl-redirect=false \
      nginx.ingress.kubernetes.io/force-ssl-redirect=false \
      --overwrite

  run_or_fail \
    "failed applying kube-vip manifests" \
    apply_kube_vip_manifests

  routing_service_ref="$(wait_for_routing_frontdoor_service || true)"
  if [[ -n "${routing_service_ref}" ]]; then
    routing_service_ns="${routing_service_ref%%/*}"
    routing_service_name="${routing_service_ref##*/}"
    desired_vip="${INGRESS_EXTERNAL_VIP:-${INGRESS_INTERNAL_VIP:-}}"
    desired_service_type="LoadBalancer"
    existing_lb_class="$(extract_ingress_lb_class "${routing_service_ns}" "${routing_service_name}")"

    echo "[phase60] setting ${routing_service_ref} type=${desired_service_type}"
    run_or_fail \
      "failed setting service type on ${routing_service_ref}" \
      "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
        -p "{\"spec\":{\"type\":\"${desired_service_type}\"}}"

    existing_lb_class="$(extract_ingress_lb_class "${routing_service_ns}" "${routing_service_name}")"
    if [[ -n "${existing_lb_class}" ]]; then
      echo "[phase60] warning: ${routing_service_ref} already has loadBalancerClass=${existing_lb_class}; Phase 50 will not mutate it" >&2
    fi

    if [[ -n "${desired_vip}" ]]; then
      echo "[phase60] pinning ${routing_service_ref} vip=${desired_vip}"
      run_or_fail \
        "failed pinning VIP on ${routing_service_ref}" \
        "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
          -p "{\"spec\":{\"loadBalancerIP\":\"${desired_vip}\"},\"metadata\":{\"annotations\":{\"kube-vip.io/loadbalancerIP\":null,\"kube-vip.io/loadbalancerIPs\":\"${desired_vip}\"}}}"
    else
      echo "[phase60] requesting ${routing_service_ref} vip via DHCP (0.0.0.0)"
      run_or_fail \
        "failed requesting DHCP VIP on ${routing_service_ref}" \
        "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
          -p "{\"spec\":{\"loadBalancerIP\":\"0.0.0.0\"},\"metadata\":{\"annotations\":{\"kube-vip.io/loadbalancerIP\":\"0.0.0.0\",\"kube-vip.io/loadbalancerIPs\":null}}}"

      concrete_vip="$(wait_for_concrete_ingress_vip "${routing_service_ns}" "${routing_service_name}" "${PHASE60_INGRESS_VIP_WAIT_TIMEOUT:-180}" "${PHASE60_INGRESS_VIP_WAIT_DELAY:-5}" || true)"
      if [[ -z "${concrete_vip}" ]]; then
        fail_local_requirement "failed waiting for concrete ingress VIP assignment on ${routing_service_ref}"
      fi
      echo "[phase60] ${routing_service_ref} assigned vip=${concrete_vip}"
      desired_vip="${concrete_vip}"
      run_or_fail \
        "failed persisting assigned VIP on ${routing_service_ref}" \
        "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
          -p "{\"spec\":{\"loadBalancerIP\":\"${desired_vip}\"},\"metadata\":{\"annotations\":{\"kube-vip.io/loadbalancerIP\":null,\"kube-vip.io/loadbalancerIPs\":\"${desired_vip}\"}}}"
    fi

    # Keep external-dns host ownership on the selected front door for public .services records.
    "${kubectl_bin}" -n "${routing_service_ns}" patch svc "${routing_service_name}" --type=merge \
      -p "{\"metadata\":{\"annotations\":{\"external-dns.alpha.kubernetes.io/hostname\":\"${external_dns_bootstrap_hosts}\"}}}" >/dev/null 2>&1 || true

    effective_pair="$(extract_ingress_lb_settings "${routing_service_ns}" "${routing_service_name}")"
    vip_eff="${effective_pair%%|*}"
    status_vip_eff="${effective_pair#*|}"

    if [[ -z "${vip_eff}" && -n "${status_vip_eff}" ]]; then
      vip_eff="${status_vip_eff}"
    fi
    if [[ -z "${vip_eff}" ]]; then
      echo "[phase60] warning: unable to determine ingress VIP from ${routing_service_ref}; skipping strict VIP/DNS IP match for local domains"
      local_domain_expected_vip=""
    else
      internal_vip_eff="${vip_eff}"
      external_vip_eff="${vip_eff}"
      local_domain_expected_vip="${vip_eff}"
      emit_dns_update_window_notice \
        "${vip_eff}" \
        "$(gitea_local_domain_retry_count)" \
        "${PHASE60_DOMAIN_CHECK_DELAY}"
    fi
    if [[ -z "${internal_vip_eff:-}" ]]; then
      internal_vip_eff="${INGRESS_INTERNAL_VIP:-}"
    fi
    if [[ -z "${external_vip_eff:-}" ]]; then
      external_vip_eff="${INGRESS_EXTERNAL_VIP:-}"
    fi

    write_secret_file ingress_internal_vip "${internal_vip_eff}"
    write_secret_file ingress_external_vip "${external_vip_eff}"

    # Store ingress VIP settings in-cluster as a normal Secret for audit/recovery export.
    cat <<EOF | "${kubectl_bin}" -n ingress apply -f - >/dev/null 2>&1 || true
apiVersion: v1
kind: Secret
metadata:
  name: ingress-vip-config
type: Opaque
stringData:
  ingress_internal_vip: "${internal_vip_eff}"
  ingress_external_vip: "${external_vip_eff}"
  ingress_controller_service: "${routing_service_ref}"
EOF
  else
    fail_local_requirement "missing authoritative ingress front-door Service after wait (expected kube-system/rke2-ingress-nginx-controller or ingress-nginx/ingress-nginx-controller)"
  fi
else
  fail_local_requirement "missing ${repo_root}/pods/ingress"
fi

external_dns_available=0

# external-dns requires a Cloudflare API token Secret (kept out of git).
if [[ -d "${repo_root}/pods/ingress/external-dns" ]]; then
  ensure_external_dns_cloudflare_secret >/dev/null 2>&1 || true
  if "${kubectl_bin}" -n ingress get secret external-dns-cloudflare >/dev/null 2>&1; then
    external_dns_available=1
    run_or_fail \
      "failed applying external-dns manifests with CLUSTER_DOMAIN=${CLUSTER_DOMAIN}" \
      apply_external_dns_manifests
  else
    echo "[phase60] skip: external-dns-cloudflare secret missing in namespace ingress"
  fi
else
  echo "[phase60] skip: missing ${repo_root}/pods/ingress/external-dns"
fi

run_or_fail \
  "failed ensuring ansible-runner image pull secret" \
  ensure_ansible_runner_pull_secret

run_or_fail \
  "failed publishing ansible-runner image to Gitea registry" \
  publish_ansible_runner_image

run_or_fail \
  "failed applying ansible-runner deployment with derived ANSIBLE_RUNNER_IMAGE" \
  apply_ansible_runner_deployment

if [[ -z "${gitea_local_host}" || -z "${argocd_local_host}" ]]; then
  fail_local_requirement "local ingress hosts were not computed"
fi

if [[ "${BOOTSTRAP_GITEA_GOLDEN_PATH_REQUIRED}" == "1" || "${BOOTSTRAP_GITEA_GOLDEN_PATH_REQUIRED}" == "true" ]]; then
  require_gitea_golden_path
fi

domain_rc=0
check_domain_with_retry "${gitea_local_host}" "${local_domain_expected_vip}" "$(gitea_local_domain_retry_count)" || domain_rc=$?
if [[ "${domain_rc}" -ne 0 ]]; then
  gitea_debug_dump "local-domain-check-${domain_rc}"
  if [[ "${domain_rc}" -eq 2 ]]; then
    if [[ "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "1" || "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "true" ]]; then
      fail_local_requirement "${gitea_local_host} returned unexpected HTTP status ${PHASE60_LAST_HTTP_CODE:-unknown} after waiting for Gitea readiness"
    fi
    echo "[phase60] warning: ${gitea_local_host} returned unexpected HTTP status ${PHASE60_LAST_HTTP_CODE:-unknown} after waiting for Gitea readiness (non-strict local mode; possible stale DNS/VIP drift)" >&2
  elif [[ "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "1" || "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "true" ]]; then
    fail_local_requirement "${gitea_local_host} failed DNS/HTTP validation after waiting for Gitea readiness"
  else
    echo "[phase60] warning: ${gitea_local_host} failed DNS/HTTP validation after waiting for Gitea readiness (non-strict local mode; possible stale DNS/VIP drift)" >&2
  fi
fi
domain_rc=0
check_domain_with_retry "${argocd_local_host}" "${local_domain_expected_vip}" "$(local_domain_retry_count)" || domain_rc=$?
if [[ "${domain_rc}" -ne 0 ]]; then
  if [[ "${domain_rc}" -eq 2 ]]; then
    if [[ "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "1" || "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "true" ]]; then
      fail_local_requirement "${argocd_local_host} returned unexpected HTTP status ${PHASE60_LAST_HTTP_CODE:-unknown} after retries"
    fi
    echo "[phase60] warning: ${argocd_local_host} returned unexpected HTTP status ${PHASE60_LAST_HTTP_CODE:-unknown} after retries (non-strict local mode; possible stale DNS/VIP drift)" >&2
  elif [[ "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "1" || "${PHASE60_LOCAL_DOMAIN_CHECK_STRICT}" == "true" ]]; then
    fail_local_requirement "${argocd_local_host} failed DNS/HTTP validation after retries"
  else
    echo "[phase60] warning: ${argocd_local_host} failed DNS/HTTP validation after retries (non-strict local mode; possible stale DNS/VIP drift)" >&2
  fi
fi

if [[ -z "${gitea_external_host}" || -z "${argocd_external_host}" ]]; then
  fail_local_requirement "external ingress hosts were not computed"
fi
if [[ "${external_dns_available}" -eq 1 ]]; then
  domain_rc=0
  check_domain_with_retry "${gitea_external_host}" "" 1 || domain_rc=$?
  if [[ "${domain_rc}" -ne 0 ]]; then
    if [[ "${domain_rc}" -eq 2 ]]; then
      echo "[phase60] warning: ${gitea_external_host} returned unexpected HTTP status ${PHASE60_LAST_HTTP_CODE:-unknown} (external warning-only mode; possible stale DNS/VIP drift)" >&2
    else
      echo "[phase60] warning: ${gitea_external_host} failed DNS/HTTP validation (external warning-only mode; possible stale DNS/VIP drift)" >&2
    fi
  fi
  domain_rc=0
  check_domain_with_retry "${argocd_external_host}" "" 1 || domain_rc=$?
  if [[ "${domain_rc}" -ne 0 ]]; then
    if [[ "${domain_rc}" -eq 2 ]]; then
      echo "[phase60] warning: ${argocd_external_host} returned unexpected HTTP status ${PHASE60_LAST_HTTP_CODE:-unknown} (external warning-only mode; possible stale DNS/VIP drift)" >&2
    else
      echo "[phase60] warning: ${argocd_external_host} failed DNS/HTTP validation (external warning-only mode; possible stale DNS/VIP drift)" >&2
    fi
  fi
else
  echo "[phase60] skipping external hostname validation because external-dns is not configured for this bootstrap run"
fi

argocd_debug_dump
require_rancher_origin_ready
require_cloudflared_ready

echo "[phase60] complete"
