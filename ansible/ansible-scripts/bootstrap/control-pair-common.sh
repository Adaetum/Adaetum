#!/usr/bin/env bash
# Shared mechanics for the phase-based bootstrap entrypoints.
# Keep phase-specific policy and logging in each entrypoint; this file owns
# reusable cluster and credential operations that must behave identically.

bootstrap_control_pair_resolve_kubectl() {
  if command -v kubectl >/dev/null 2>&1; then
    command -v kubectl
    return 0
  fi
  if [[ -x /var/lib/rancher/rke2/bin/kubectl ]]; then
    printf '%s\n' /var/lib/rancher/rke2/bin/kubectl
    return 0
  fi
  return 1
}

bootstrap_control_pair_prepare_repo() {
  local repo_root="$1"
  cd "${repo_root}"
  if [[ -z "${ANSIBLE_CONFIG:-}" && -f "${repo_root}/ansible/ansible.cfg" ]]; then
    export ANSIBLE_CONFIG="${repo_root}/ansible/ansible.cfg"
  fi
  export ANSIBLE_ROLES_PATH="${repo_root}/ansible/automation-roles:${repo_root}/ansible/playbooks/roles:/etc/ansible/roles:/usr/share/ansible/roles"
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

# Helpers below are extracted only when both phase entrypoints use the exact same implementation.

# Credentials, secrets, and small data helpers

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
read_configmap_key_plain() {
  local namespace="$1"
  local configmap_name="$2"
  local key="$3"
  "${kubectl_bin}" -n "${namespace}" get configmap "${configmap_name}" \
    -o "jsonpath={.data.${key}}" 2>/dev/null | tr -d '\r\n' || true
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
persist_openbao_homepage_field() {
  local field="${1:-}"
  local value="${2:-}"
  local openbao_token="${3:-}"
  local secret_key=""

  if [[ -z "${field}" || -z "${value}" || -z "${openbao_token}" || -z "${kubectl_bin:-}" ]]; then
    return 0
  fi

  case "${field}" in
    homepage_argocd_widget_key) secret_key="HOMEPAGE_ARGOCD_WIDGET_KEY" ;;
    homepage_gitea_widget_auth) secret_key="HOMEPAGE_GITEA_WIDGET_AUTH" ;;
    *) return 0 ;;
  esac

  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
    bao kv patch secret/apps/homepage/widgets "${secret_key}=${value}" >/dev/null 2>&1 || \
  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
    bao kv put secret/apps/homepage/widgets "${secret_key}=${value}" >/dev/null
}

read_openbao_app_field() {
  local path="${1:-}"
  local field="${2:-}"
  local openbao_token="${3:-}"

  if [[ -z "${path}" || -z "${field}" || -z "${openbao_token}" || -z "${kubectl_bin:-}" ]]; then
    return 0
  fi
  "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
    bao kv get -field="${field}" "secret/apps/${path}" 2>/dev/null | tr -d '\r\n' || true
}

# Gitea access tokens are application-issued credentials, not arbitrary KV
# strings. These helpers let late bootstrap prove the selected Homepage token
# has only the intended read scopes and revoke superseded system-owned tokens
# after the replacement has been promoted to OpenBao and Kubernetes.
gitea_widget_token_has_required_scopes() {
  local base_url="${1:-}"
  local username="${2:-}"
  local password="${3:-}"
  local token="${4:-}"
  local inventory=""

  if [[ -z "${base_url}" || -z "${username}" || -z "${password}" || -z "${token}" ]]; then
    return 1
  fi
  inventory="$(
    curl --silent --show-error --fail --max-time 20 \
      --user "${username}:${password}" \
      "${base_url}/api/v1/users/${username}/tokens?limit=100" 2>/dev/null || true
  )"
  [[ -n "${inventory}" ]] || return 1

  GITEA_TOKEN_INVENTORY="${inventory}" python3 - "${token}" <<'PY'
import json
import os
import sys

token = sys.argv[1]
required = {"read:notification", "read:repository", "read:issue"}
try:
    tokens = json.loads(os.environ["GITEA_TOKEN_INVENTORY"])
except (TypeError, ValueError):
    raise SystemExit(1)

suffix = token[-8:]
for entry in tokens:
    if entry.get("token_last_eight") == suffix:
        raise SystemExit(0 if set(entry.get("scopes") or []) == required else 1)
raise SystemExit(1)
PY
}

revoke_stale_gitea_widget_tokens() {
  local base_url="${1:-}"
  local username="${2:-}"
  local password="${3:-}"
  local keep_token="${4:-}"
  local inventory=""
  local stale_ids=""
  local token_id=""

  if [[ -z "${base_url}" || -z "${username}" || -z "${password}" || -z "${keep_token}" ]]; then
    return 1
  fi
  inventory="$(
    curl --silent --show-error --fail --max-time 20 \
      --user "${username}:${password}" \
      "${base_url}/api/v1/users/${username}/tokens?limit=100" 2>/dev/null || true
  )"
  [[ -n "${inventory}" ]] || return 1

  stale_ids="$(GITEA_TOKEN_INVENTORY="${inventory}" python3 - "${keep_token}" <<'PY'
import json
import os
import sys

keep_suffix = sys.argv[1][-8:]
try:
    tokens = json.loads(os.environ["GITEA_TOKEN_INVENTORY"])
except (TypeError, ValueError):
    raise SystemExit(1)

for entry in tokens:
    name = str(entry.get("name") or "")
    if name.startswith("homepage-widget") and entry.get("token_last_eight") != keep_suffix:
        token_id = entry.get("id")
        if isinstance(token_id, int):
            print(token_id)
PY
  )" || return 1

  for token_id in ${stale_ids}; do
    curl --silent --show-error --fail --max-time 20 \
      --user "${username}:${password}" \
      --request DELETE \
      --output /dev/null \
      "${base_url}/api/v1/users/${username}/tokens/${token_id}" || return 1
  done

  # Re-read the registry so a successful return proves both least privilege
  # and revocation rather than merely proving that DELETE returned no output.
  gitea_widget_token_has_required_scopes \
    "${base_url}" "${username}" "${password}" "${keep_token}" || return 1
  inventory="$(
    curl --silent --show-error --fail --max-time 20 \
      --user "${username}:${password}" \
      "${base_url}/api/v1/users/${username}/tokens?limit=100" 2>/dev/null || true
  )"
  GITEA_TOKEN_INVENTORY="${inventory}" python3 - "${keep_token}" <<'PY'
import json
import os
import sys

keep_suffix = sys.argv[1][-8:]
tokens = json.loads(os.environ["GITEA_TOKEN_INVENTORY"])
stale = [
    entry for entry in tokens
    if str(entry.get("name") or "").startswith("homepage-widget")
    and entry.get("token_last_eight") != keep_suffix
]
raise SystemExit(1 if stale else 0)
PY
}

seed_openbao_app_fields() {
  local path="${1:-}"
  local openbao_token="${2:-}"
  shift 2 || true
  local pair=""
  local field=""
  local existing=""
  local -a missing=()

  if [[ -z "${path}" || -z "${openbao_token}" || -z "${kubectl_bin:-}" || "$#" -eq 0 ]]; then
    return 0
  fi

  for pair in "$@"; do
    field="${pair%%=*}"
    existing="$(read_openbao_app_field "${path}" "${field}" "${openbao_token}")"
    if [[ -z "${existing}" ]]; then
      missing+=("${pair}")
    fi
  done
  if [[ "${#missing[@]}" -eq 0 ]]; then
    return 0
  fi

  retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
    env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
    bao kv patch "secret/apps/${path}" "${missing[@]}" >/dev/null 2>&1 || \
    retry_cmd 6 5 "${kubectl_bin}" -n "${OPENBAO_NAMESPACE}" exec -i "${OPENBAO_POD}" -- \
      env BAO_ADDR="http://127.0.0.1:8200" BAO_TOKEN="${openbao_token}" \
      bao kv put "secret/apps/${path}" "${missing[@]}" >/dev/null
}
# Ansible-runner image and registry helpers

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
# Gitea access and cluster-readiness helpers

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
  token="$(read_openbao_app_field gitea/registry token "${openbao_token:-}" || true)"
  if [[ -n "${token}" ]]; then
    write_secret_file gitea_registry_token "${token}"
    printf '%s' "${token}"
    return 0
  fi

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
  token="$(read_openbao_app_field argocd/repository password "${openbao_token:-}" || true)"
  if [[ -n "${token}" ]]; then
    write_secret_file gitea_git_token "${token}"
    printf '%s' "${token}"
    return 0
  fi

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
# Ingress address helpers

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
# GitOps repository and Application helpers

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

# Install the recovery-mirror hook without persisting its credential on Gitea's
# repository PVC. The hook reads the ESO delivery Secret on every push, so a
# refresh takes effect without rewriting application state or restarting Gitea.
configure_gitea_push_mirror_from_openbao() {
  local phase_label="${1:?phase label}"
  local target_owner="${2:?owner}"
  local target_repo="${3:?repo}"
  local mirror_repo_url="${4:-}"
  local mirror_username="${5:-}"
  local mirror_token="${6:-}"
  local openbao_repo_url=""
  local openbao_username=""
  local openbao_token_value=""
  local expected_token_sha256=""
  local pod=""

  # On a rerun, OpenBao is authoritative. Local bootstrap inputs may be stale
  # after an operator rotates the provider-issued GitHub credential.
  if [[ -n "${openbao_token:-}" ]]; then
    openbao_token_value="$(read_openbao_app_field gitea/push-mirror token "${openbao_token}" || true)"
    if [[ -n "${openbao_token_value}" ]]; then
      openbao_repo_url="$(read_openbao_app_field gitea/push-mirror remote_url "${openbao_token}" || true)"
      openbao_username="$(read_openbao_app_field gitea/push-mirror username "${openbao_token}" || true)"
      mirror_token="${openbao_token_value}"
      mirror_repo_url="${openbao_repo_url:-${mirror_repo_url}}"
      mirror_username="${openbao_username:-${mirror_username}}"
    fi
  fi

  if [[ -z "${mirror_repo_url}" || -z "${mirror_username}" || -z "${mirror_token}" ]]; then
    echo "[${phase_label}] Gitea push mirror is not fully configured; skipping" >&2
    return 0
  fi
  if ! repo_url_is_github "${mirror_repo_url}"; then
    echo "[${phase_label}] Gitea push mirror target is not a GitHub repo; skipping auto mirror setup" >&2
    return 0
  fi
  if ! github_token_looks_like_pat "${mirror_token}"; then
    echo "[${phase_label}] Gitea push mirror requires a stable GitHub PAT-style token; skipping auto mirror setup" >&2
    return 0
  fi

  # Seed only when the app path is absent. Once present, the reads above ensure
  # a stale node-local value can never replace OpenBao during a rerun.
  if [[ -z "${openbao_token_value}" && -n "${openbao_token:-}" ]]; then
    seed_openbao_app_fields gitea/push-mirror "${openbao_token}" \
      "remote_url=${mirror_repo_url}" \
      "username=${mirror_username}" \
      "token=${mirror_token}"
  fi

  # This remains an ESO adapter because Gitea's hook uses a projected native
  # Secret. Once OpenBao is seeded, wait for the adapter instead of reviving a
  # bootstrap writer on reruns.
  bootstrap_wait_for_external_secret_delivery \
    "${kubectl_bin}" gitea gitea-push-mirror gitea-push-mirror gitea-push-mirror

  pod="$(find_ready_gitea_pod)" || {
    echo "[${phase_label}] no ready Gitea pod found for push mirror setup" >&2
    return 1
  }
  expected_token_sha256="$(printf '%s' "${mirror_token}" | sha256sum | awk '{print $1}')"

  if ! "${kubectl_bin}" -n gitea exec -i "${pod}" -c gitea -- env \
    TARGET_OWNER="${target_owner}" \
    TARGET_REPO="${target_repo}" \
    EXPECTED_REPO_URL="${mirror_repo_url}" \
    EXPECTED_USERNAME="${mirror_username}" \
    EXPECTED_TOKEN_SHA256="${expected_token_sha256}" \
    sh -s -- <<'EOF'
set -eu

target_owner="${TARGET_OWNER:?}"
target_repo="${TARGET_REPO:?}"
credential_dir="/var/run/adaetum/push-mirror"

# The Secret may have been created after this pod started. Kubelet refreshes
# projected Secret volumes asynchronously, so wait for the exact reviewed
# credential without passing the credential itself through pod exec.
credential_ready=false
for _ in $(seq 1 90); do
  if [ -r "${credential_dir}/remote_url" ] \
    && [ -r "${credential_dir}/username" ] \
    && [ -r "${credential_dir}/token" ] \
    && [ "$(cat "${credential_dir}/remote_url")" = "${EXPECTED_REPO_URL}" ] \
    && [ "$(cat "${credential_dir}/username")" = "${EXPECTED_USERNAME}" ] \
    && [ "$(sha256sum "${credential_dir}/token" | awk '{print $1}')" = "${EXPECTED_TOKEN_SHA256}" ]; then
    credential_ready=true
    break
  fi
  sleep 2
done
if [ "${credential_ready}" != true ]; then
  echo "OpenBao delivery did not reach Gitea's projected push-mirror volume" >&2
  exit 1
fi

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
  echo "missing Gitea repository path for ${target_owner}/${target_repo}" >&2
  exit 1
fi

hooks_dir="${repo_path}/hooks"
mkdir -p "${hooks_dir}"

# Remove credentials written by the legacy hook implementation. They are no
# longer an authority or recovery copy once the projected Secret is available.
rm -f \
  "${hooks_dir}/.github-mirror.username" \
  "${hooks_dir}/.github-mirror.password" \
  "${hooks_dir}/.github-mirror.url"

cat >"${hooks_dir}/.github-mirror-askpass.sh" <<'EOF_ASKPASS'
#!/bin/sh
credential_dir=/var/run/adaetum/push-mirror
case "$1" in
  *sername*) cat "${credential_dir}/username" ;;
  *) cat "${credential_dir}/token" ;;
esac
EOF_ASKPASS
chmod 0700 "${hooks_dir}/.github-mirror-askpass.sh"

cat >"${hooks_dir}/post-receive" <<'EOF_HOOK'
#!/bin/sh
set -eu
(
  hooks_dir="$(dirname "$0")"
  repo_dir="$(dirname "${hooks_dir}")"
  credential_dir=/var/run/adaetum/push-mirror
  export GIT_TERMINAL_PROMPT=0
  export GIT_ASKPASS="${hooks_dir}/.github-mirror-askpass.sh"
  git -C "${repo_dir}" push --mirror "$(cat "${credential_dir}/remote_url")" \
    >>"${hooks_dir}/github-mirror.log" 2>&1 || true
) &
EOF_HOOK
chmod 0700 "${hooks_dir}/post-receive"

export GIT_TERMINAL_PROMPT=0
export GIT_ASKPASS="${hooks_dir}/.github-mirror-askpass.sh"
if ! git -C "${repo_path}" push --mirror "$(cat "${credential_dir}/remote_url")" \
  >>"${hooks_dir}/github-mirror.log" 2>&1; then
  echo "initial recovery-mirror push failed" >&2
  tail -n 20 "${hooks_dir}/github-mirror.log" >&2 || true
  exit 1
fi
EOF
  then
    echo "[${phase_label}] failed to configure Gitea push mirror" >&2
    return 1
  fi

  echo "[${phase_label}] configured OpenBao-backed Gitea push mirror"
}
# Domain and host normalization helpers

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
# Ingress controller discovery helpers

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

# A rollout timeout is not itself a failure signal: provisioning a first PVC,
# pulling an image, or initializing a database can take very different amounts
# of time on supported hardware. Keep watching while Kubernetes reports normal
# progress, but stop immediately and preserve evidence once the Deployment
# controller declares the rollout terminal.
bootstrap_capture_deployment_rollout_diagnostics() {
  local kubectl_path="${1:?kubectl path}"
  local namespace="${2:?namespace}"
  local deployment="${3:?deployment}"
  local component="${4:-${deployment}}"
  local pod_selector="${5:-}"
  local evidence_path=""
  local pod_name=""

  evidence_path="$(bootstrap_diag_capture_evidence_path "${component}" "rollout")"
  {
    echo "[rollout] diagnostics for ${namespace}/deploy/${deployment} ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
    echo "[rollout] deployment condition and replica status:"
    "${kubectl_path}" -n "${namespace}" get deployment "${deployment}" -o wide || true
    "${kubectl_path}" -n "${namespace}" describe deployment "${deployment}" || true
    echo "[rollout] relevant pods:"
    if [[ -n "${pod_selector}" ]]; then
      "${kubectl_path}" -n "${namespace}" get pods --selector="${pod_selector}" -o wide || true
    else
      "${kubectl_path}" -n "${namespace}" get pods -o wide || true
    fi
    echo "[rollout] persistent-volume claims:"
    "${kubectl_path}" -n "${namespace}" get pvc -o wide || true
    # Structural Kubernetes API consumers still receive OpenBao values through
    # External Secrets. Preserve that adapter's state alongside CSI evidence so
    # recovery logs identify the actual broken delivery path.
    echo "[rollout] External Secrets delivery state:"
    "${kubectl_path}" -n "${namespace}" get externalsecret -o wide || true
    "${kubectl_path}" -n "${namespace}" describe externalsecret || true
    echo "[rollout] OpenBao ClusterSecretStore state:"
    "${kubectl_path}" get clustersecretstore openbao -o wide || true
    "${kubectl_path}" describe clustersecretstore openbao || true
    echo "[rollout] External Secrets controller logs:"
    "${kubectl_path}" -n external-secrets logs deploy/external-secrets --all-containers --tail=200 || true
    echo "[rollout] recent namespace events:"
    "${kubectl_path}" -n "${namespace}" get events --sort-by=.lastTimestamp | tail -n 100 || true
    while IFS= read -r pod_name; do
      [[ -n "${pod_name}" ]] || continue
      echo "[rollout] describe pod/${pod_name}:"
      "${kubectl_path}" -n "${namespace}" describe pod "${pod_name}" || true
      echo "[rollout] current logs for pod/${pod_name}:"
      "${kubectl_path}" -n "${namespace}" logs "${pod_name}" --all-containers --tail=200 || true
      echo "[rollout] previous logs for pod/${pod_name}:"
      "${kubectl_path}" -n "${namespace}" logs "${pod_name}" --all-containers --previous --tail=200 || true
    done < <(
      if [[ -n "${pod_selector}" ]]; then
        "${kubectl_path}" -n "${namespace}" get pods --selector="${pod_selector}" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null
      else
        "${kubectl_path}" -n "${namespace}" get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null
      fi
    )
  } | tee "${evidence_path}" || true

  bootstrap_diag_record_file_event \
    "${component}" "${deployment}-rollout" "captured deployment, pod, PVC, External Secrets, event, and container-log diagnostics" \
    "${evidence_path}" "error" "rollout"
}

# GitOps can create a workload before ESO has delivered its OpenBao-backed
# Kubernetes Secret. Gate dependent work on ESO's Ready condition instead of
# spending later phases on a pod that Kubernetes already knows cannot start.
# There is intentionally no wall-clock deadline: a controller that is still
# starting is allowed to converge, while a reported sync error is actionable.
# A pre-existing Kubernetes Secret is never sufficient on its own: OpenBao via
# ESO is the sole production authority for workload credentials.
bootstrap_wait_for_external_secret_delivery() {
  local kubectl_path="${1:?kubectl path}"
  local namespace="${2:?namespace}"
  local external_secret="${3:?ExternalSecret name}"
  local target_secret="${4:?target Secret name}"
  local component="${5:-${external_secret}}"
  local ready_status=""
  local ready_reason=""
  local ready_message=""
  local evidence_path=""
  local target_exists="false"

  echo "[secret-sync] watching ${namespace}/externalsecret/${external_secret} -> secret/${target_secret}"
  if ! "${kubectl_path}" -n "${namespace}" get externalsecret "${external_secret}" >/dev/null 2>&1; then
    echo "[secret-sync] ${namespace}/externalsecret/${external_secret}: expected delivery declaration is absent" >&2
    evidence_path="$(bootstrap_diag_capture_evidence_path "${component}" "external-secret")"
    {
      echo "[secret-sync] diagnostics for missing ${namespace}/externalsecret/${external_secret} ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
      "${kubectl_path}" -n "${namespace}" get externalsecrets -o wide || true
      "${kubectl_path}" get clustersecretstore openbao -o wide || true
      "${kubectl_path}" -n external-secrets get pods -o wide || true
    } | tee "${evidence_path}" || true
    return 1
  fi
  while true; do
    if "${kubectl_path}" -n "${namespace}" get secret "${target_secret}" >/dev/null 2>&1; then
      target_exists="true"
    fi

    ready_status="$("${kubectl_path}" -n "${namespace}" get externalsecret "${external_secret}" -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{"\\t"}{.reason}{"\\t"}{.message}{end}' 2>/dev/null || true)"
    if [[ "${ready_status}" == False$'\t'* ]]; then
      ready_reason="${ready_status#*$'\t'}"
      ready_reason="${ready_reason%%$'\t'*}"
      ready_message="${ready_status##*$'\t'}"
      echo "[secret-sync] ${namespace}/externalsecret/${external_secret}: terminal ${ready_reason}: ${ready_message}" >&2
      evidence_path="$(bootstrap_diag_capture_evidence_path "${component}" "external-secret")"
      {
        echo "[secret-sync] diagnostics for ${namespace}/externalsecret/${external_secret} ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
        "${kubectl_path}" -n "${namespace}" describe externalsecret "${external_secret}" || true
        "${kubectl_path}" get clustersecretstore openbao -o wide || true
        "${kubectl_path}" describe clustersecretstore openbao || true
        "${kubectl_path}" -n external-secrets logs deploy/external-secrets --all-containers --tail=200 || true
      } | tee "${evidence_path}" || true
      bootstrap_diag_record_file_event \
        "${component}" "${external_secret}-sync" "captured ExternalSecret, OpenBao store, and controller diagnostics" \
        "${evidence_path}" "error" "secret-sync"
      return 1
    fi

    if [[ "${target_exists}" == "true" ]]; then
      if [[ "${ready_status}" == True$'\t'* ]]; then
        echo "[secret-sync] ${namespace}/secret/${target_secret}: ready and ESO-backed"
        return 0
      fi
    fi

    echo "[secret-sync] ${namespace}/externalsecret/${external_secret}: controller is still reconciling"
    sleep 15
  done
}

# CSI is the direct OpenBao delivery path. A SecretProviderClass is declarative
# intent; the PodStatus is the proof that kubelet mounted the value using the
# requesting workload identity. Do not accept a similarly named Kubernetes
# Secret here because that would reintroduce a second authority.
bootstrap_wait_for_csi_secret_delivery() {
  local kubectl_path="${1:?kubectl path}"
  local namespace="${2:?namespace}"
  local provider_class="${3:?SecretProviderClass name}"
  local component="${4:-${provider_class}}"
  local expected_pod="${5:-}"
  local status=""
  local mount_failure=""
  local evidence_path=""

  echo "[secret-csi] watching ${namespace}/secretproviderclass/${provider_class}"
  while true; do
    if ! "${kubectl_path}" -n "${namespace}" get secretproviderclass "${provider_class}" >/dev/null 2>&1; then
      echo "[secret-csi] ${namespace}/secretproviderclass/${provider_class}: waiting for GitOps apply"
      sleep 15
      continue
    fi
    status="$("${kubectl_path}" -n "${namespace}" get secretproviderclasspodstatus \
      -o jsonpath='{range .items[?(@.spec.secretProviderClassName=="'"${provider_class}"'")]}{.metadata.name}{"\t"}{.spec.podName}{"\\n"}{end}' 2>/dev/null || true)"
    if [[ -n "${expected_pod}" ]]; then
      status="$(printf '%s\n' "${status}" | awk -F '\t' -v pod="${expected_pod}" '$2 == pod { print $1 }')"
    fi
    if [[ -n "${status}" ]]; then
      echo "[secret-csi] ${namespace}/secretproviderclass/${provider_class}: mounted by ${status//$'\n'/, }"
      return 0
    fi
    mount_failure="$("${kubectl_path}" -n "${namespace}" get events --sort-by=.lastTimestamp 2>/dev/null | grep -E 'FailedMount|MountVolume\.SetUp failed|permission denied|forbidden' | tail -n 1 || true)"
    if [[ -n "${mount_failure}" ]]; then
      echo "[secret-csi] ${namespace}/secretproviderclass/${provider_class}: terminal mount failure: ${mount_failure}" >&2
      evidence_path="$(bootstrap_diag_capture_evidence_path "${component}" "csi-secret")"
      {
        echo "[secret-csi] diagnostics for ${namespace}/secretproviderclass/${provider_class} ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
        echo "${mount_failure}"
        "${kubectl_path}" -n "${namespace}" describe secretproviderclass "${provider_class}" || true
        "${kubectl_path}" -n "${namespace}" get secretproviderclasspodstatus -o wide || true
        "${kubectl_path}" -n kube-system get pods -o wide | grep -E 'secrets-store|openbao.*csi' || true
        "${kubectl_path}" -n kube-system logs -l app.kubernetes.io/component=csi --all-containers --tail=200 || true
      } | tee "${evidence_path}" || true
      bootstrap_diag_record_file_event \
        "${component}" "${provider_class}-csi" "captured CSI class, mount, provider, and event diagnostics" \
        "${evidence_path}" "error" "secret-csi"
      return 1
    fi
    evidence_path="$(bootstrap_diag_capture_evidence_path "${component}" "csi-secret")"
    {
      echo "[secret-csi] diagnostics for ${namespace}/secretproviderclass/${provider_class} ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
      "${kubectl_path}" -n "${namespace}" describe secretproviderclass "${provider_class}" || true
      "${kubectl_path}" -n "${namespace}" get secretproviderclasspodstatus -o wide || true
      "${kubectl_path}" -n kube-system get pods -o wide | grep -E 'secrets-store|openbao.*csi' || true
      "${kubectl_path}" -n kube-system logs -l app.kubernetes.io/component=csi --all-containers --tail=100 || true
    } >"${evidence_path}" 2>&1 || true
    echo "[secret-csi] ${namespace}/secretproviderclass/${provider_class}: waiting for first authenticated mount"
    sleep 15
  done
}

bootstrap_wait_for_deployment_rollout() {
  local kubectl_path="${1:?kubectl path}"
  local namespace="${2:?namespace}"
  local deployment="${3:?deployment}"
  local component="${4:-${deployment}}"
  local pod_selector="${5:-}"
  local rollout_output=""
  local progressing_reason=""
  local replica_failure=""

  echo "[rollout] watching ${namespace}/deploy/${deployment}; waiting for Kubernetes readiness"
  while true; do
    if rollout_output="$("${kubectl_path}" -n "${namespace}" rollout status "deploy/${deployment}" --timeout=45s 2>&1)"; then
      echo "[rollout] ${namespace}/deploy/${deployment}: Ready"
      return 0
    fi

    if ! "${kubectl_path}" -n "${namespace}" get deployment "${deployment}" >/dev/null 2>&1; then
      echo "[rollout] ${namespace}/deploy/${deployment}: terminal deployment missing"
      bootstrap_capture_deployment_rollout_diagnostics \
        "${kubectl_path}" "${namespace}" "${deployment}" "${component}" "${pod_selector}"
      return 1
    fi

    progressing_reason="$("${kubectl_path}" -n "${namespace}" get deployment "${deployment}" -o jsonpath='{range .status.conditions[?(@.type=="Progressing")]}{.reason}{" "}{.message}{end}' 2>/dev/null || true)"
    replica_failure="$("${kubectl_path}" -n "${namespace}" get deployment "${deployment}" -o jsonpath='{range .status.conditions[?(@.type=="ReplicaFailure")]}{.status}{" "}{.reason}{" "}{.message}{end}' 2>/dev/null || true)"
    if [[ "${rollout_output}" == *"exceeded its progress deadline"* || "${progressing_reason}" == *"ProgressDeadlineExceeded"* || "${replica_failure}" == True* ]]; then
      echo "[rollout] ${namespace}/deploy/${deployment}: terminal ${progressing_reason:-${replica_failure:-failure}}"
      bootstrap_capture_deployment_rollout_diagnostics \
        "${kubectl_path}" "${namespace}" "${deployment}" "${component}" "${pod_selector}"
      return 1
    fi

    echo "[rollout] ${namespace}/deploy/${deployment}: still progressing (${rollout_output:-no rollout response})"
    if [[ -n "${pod_selector}" ]]; then
      "${kubectl_path}" -n "${namespace}" get pods --selector="${pod_selector}" -o wide 2>/dev/null | tail -n +2 | head -n 3 || true
    fi
  done
}
