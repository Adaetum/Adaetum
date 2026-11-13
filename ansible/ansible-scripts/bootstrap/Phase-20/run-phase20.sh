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

write_ntfy_publisher_password() {
  local name="ntfy_apprise_password"
  local path="${BOOTSTRAP_SECRET_DIR}/${name}"
  if [[ -s "${path}" ]]; then
    if grep -Eq '^[A-Za-z0-9_-]{32}$' "${path}"; then
      echo "[skip] ${name} already exists"
      return 0
    fi
    echo "[error] ${name} exists but is not a valid URL-safe password: ${path}" >&2
    return 2
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\r\n' | cut -c1-32 >"${path}"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import secrets,string; alphabet=string.ascii_letters+string.digits+"-_"; print("".join(secrets.choice(alphabet) for _ in range(32)), end="")' >"${path}"
  else
    echo "[error] no supported random generator found (need openssl or python3)" >&2
    return 3
  fi
  chmod 0600 "${path}"
  echo "[ok] wrote ${path}"
}

# Make the operator's recovery password the way into this host.
#
# The password chosen in `task init` opens the emergency recovery kit, which already holds
# every other secret, so it is allowed to be the credential for this machine as well:
#   - /etc/adaetum/agent-token: the host agent's access token. The sign-in page's token
#     field accepts the password, and so does the API.
#   - /var/lib/adaetum/auth/setup-code: the code that claims a machine, which lets whoever
#     holds the password create the first owner account.
# Both are root-only files. The random token and generated setup code the image made at
# install time are replaced; they are the fallback only where no password reaches the host
# (it must be printable ASCII, since it travels in an HTTP header).
#
# The password is read from the first-boot runtime payload, which burn-the-ladder removes
# later, so this has to happen now. Reruns write the same values, and the setup code is
# seeded once so that a claimed machine does not get its code back.
seed_recovery_access() {
  local pass="${BOOTSTRAP_BACKUP_PASSPHRASE:-}"
  if [[ -z "${pass}" && -n "${BOOTSTRAP_BACKUP_PASSPHRASE_B64:-}" ]] && command -v base64 >/dev/null 2>&1; then
    pass="$(printf '%s' "${BOOTSTRAP_BACKUP_PASSPHRASE_B64}" | base64 -d 2>/dev/null || true)"
  fi
  if [[ -z "${pass}" ]]; then
    echo "[skip] recovery access: no recovery password reached this host"
    return 0
  fi
  # The C locale makes ' -~' mean exactly printable ASCII and counts bytes, not characters.
  local LC_ALL=C
  if [[ "${#pass}" -lt 16 ]] || [[ ! "${pass}" =~ ^[\ -~]+$ ]] || [[ "${pass}" != "${pass# }" ]] || [[ "${pass}" != "${pass% }" ]]; then
    echo "[warn] recovery access: the recovery password is not 16 or more printable ASCII characters, so this host keeps its own random token and setup code"
    return 0
  fi

  local token_file="${ADAETUM_AGENT_TOKEN_FILE:-/etc/adaetum/agent-token}"
  local auth_dir="${ADAETUM_AUTH_STATE_DIR:-/var/lib/adaetum/auth}"
  local seeded_marker="${auth_dir}/.recovery-seeded"
  local changed=0

  install -d -m 0750 "$(dirname "${token_file}")"
  local previous_token=""
  if [[ "$(cat "${token_file}" 2>/dev/null || true)" != "${pass}" ]]; then
    previous_token="$(cat "${token_file}" 2>/dev/null || true)"
    ( umask 077; printf '%s\n' "${pass}" >"${token_file}.new" )
    mv -f "${token_file}.new" "${token_file}"
    chmod 0600 "${token_file}"
    changed=1
    echo "[ok] wrote ${token_file} from the recovery password"
  fi

  if [[ -x /usr/local/bin/adaetum-auth && ! -e "${seeded_marker}" ]]; then
    install -d -m 0700 "${auth_dir}"
    ( umask 077; printf '%s\n' "${pass}" >"${auth_dir}/setup-code" )
    chmod 0600 "${auth_dir}/setup-code"
    : >"${seeded_marker}"
    changed=1
    echo "[ok] wrote ${auth_dir}/setup-code from the recovery password"
  fi

  # The agent and the sign-in service read these files once, at start.
  if [[ "${changed}" == "1" ]] && command -v systemctl >/dev/null 2>&1; then
    for unit in adaetum-auth.service adaetum-agent.service; do
      if systemctl is-enabled --quiet "${unit}" 2>/dev/null; then
        systemctl restart "${unit}" || echo "[warn] could not restart ${unit}; it will use the new values at its next start"
      fi
    done
    # An agent built before this change refuses a token shorter than 32 characters and exits.
    # Never leave a host without its agent over that: put the previous token back.
    if [[ -n "${previous_token}" ]] && systemctl is-enabled --quiet adaetum-agent.service 2>/dev/null; then
      sleep 4
      if ! systemctl is-active --quiet adaetum-agent.service; then
        echo "[warn] recovery access: the installed agent did not accept the recovery password as its token; restoring the previous token"
        ( umask 077; printf '%s\n' "${previous_token}" >"${token_file}" )
        chmod 0600 "${token_file}"
        systemctl restart adaetum-agent.service || true
      fi
    fi
  fi
}

write_secret "rke2_token" 48
write_secret "rancher_admin_password" 24
write_secret "argocd_admin_password" 24
write_secret "argocd_server_secret_key" 48
write_secret "argocd_redis_password" 24
write_secret "gitea_admin_password" 24
write_secret "gitea_secret_key" 48
write_secret "gitea_internal_token" 48
write_secret "gitea_jwt_secret" 48
write_secret "grafana_admin_password" 24
write_secret "grafana_secret_key" 48
write_secret "ntfy_admin_password" 24
write_ntfy_publisher_password
write_secret "authentik_secret_key" 48
write_secret "authentik_postgresql_password" 24
write_secret "authentik_admin_password" 24
write_secret "authentik_bootstrap_token" 32
write_literal_secret "authentik_admin_username" "akadmin"
# The always-active break-glass owner of the Adaetum agent (see adaetum/auth/src/breakglass.ts).
# Generated here so it lands in the emergency kit with every other first-run secret, and
# seeded into OpenBao by Phase 40, which is where its password is read and rotated.
write_literal_secret "adaetum_owner_email" "break-glass@adaetum.local"
write_secret "adaetum_owner_password" 24
seed_recovery_access

cat <<'INFO'

Bootstrap secrets generated. These are local-only and disposable.
Do not copy them off-node. Use them only for Phase 30 automation.

INFO
