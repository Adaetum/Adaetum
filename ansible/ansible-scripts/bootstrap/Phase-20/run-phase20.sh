#!/usr/bin/env bash
set -euo pipefail

# Phase 20 creates the temporary local secret authority required before OpenBao
# is available. Existing non-empty files are intentionally preserved so reruns
# do not desynchronize installed components from their bootstrap credentials.

BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
BOOTSTRAP_OWNER="${BOOTSTRAP_OWNER:-root:root}"

mkdir -p "${BOOTSTRAP_SECRET_DIR}"
chown "${BOOTSTRAP_OWNER}" "${BOOTSTRAP_SECRET_DIR}"
chmod 0700 "${BOOTSTRAP_SECRET_DIR}"

write_secret() {
  local name="$1"
  local length="$2"
  local path="${BOOTSTRAP_SECRET_DIR}/${name}"
  if [[ -f "${path}" ]]; then
    if [[ -s "${path}" ]]; then
      echo "[skip] ${name} already exists"
      return 0
    fi
    # Empty secret files are almost always a bug (e.g. a placeholder created earlier).
    # Do not silently overwrite because that can desync installed workloads from recorded bootstrap secrets.
    regen="${BOOTSTRAP_REGENERATE_EMPTY_SECRETS:-0}"
    regen="$(printf '%s' "${regen}" | tr '[:upper:]' '[:lower:]')"
    if [[ "${regen}" != "1" && "${regen}" != "true" ]]; then
      echo "[error] ${name} exists but is empty: ${path}" >&2
      echo "Fix: delete the empty file and re-run Phase 20, or set BOOTSTRAP_REGENERATE_EMPTY_SECRETS=1 to overwrite." >&2
      return 2
    fi
    echo "[warn] ${name} exists but is empty; regenerating due to BOOTSTRAP_REGENERATE_EMPTY_SECRETS=1"
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 "${length}" | tr -d '\n' >"${path}"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c '
import secrets
import string
import sys
length = int(sys.argv[1])
alphabet = string.ascii_letters + string.digits + "-_"
print("".join(secrets.choice(alphabet) for _ in range(length)), end="")
' "${length}" >"${path}"
  else
    echo "[error] no supported random generator found (need openssl or python3)" >&2
    return 3
  fi
  chmod 0600 "${path}"
  echo "[ok] wrote ${path}"
}

write_literal_secret() {
  local name="$1"
  local value="$2"
  local path="${BOOTSTRAP_SECRET_DIR}/${name}"
  if [[ -s "${path}" ]]; then
    echo "[skip] ${name} already exists"
    return 0
  fi
  printf '%s' "${value}" >"${path}"
  chmod 0600 "${path}"
  echo "[ok] wrote ${path}"
}

write_secret "rke2_token" 48
write_secret "rancher_admin_password" 24
write_secret "argocd_admin_password" 24
write_secret "gitea_admin_password" 24
write_secret "gitea_runner_token" 32
write_secret "grafana_admin_password" 24
write_secret "authentik_secret_key" 48
write_secret "authentik_postgresql_password" 24
write_secret "authentik_admin_password" 24
write_secret "authentik_bootstrap_token" 32
write_literal_secret "authentik_admin_username" "akadmin"

cat <<'INFO'

Bootstrap secrets generated. These are local-only and disposable.
Do not copy them off-node. Use them only for Phase 30 automation.

INFO
