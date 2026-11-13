#!/usr/bin/env bash
set -euo pipefail

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

seed_recovery_access
