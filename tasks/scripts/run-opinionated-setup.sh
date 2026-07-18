#!/usr/bin/env bash
set -euo pipefail

# The supported private-recovery setup entrypoint. It asks only for runtime secrets,
# derives public values from platform.yaml, and commits rendered GitOps output
# before the break-glass bundle creates a cluster from this repository.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
# shellcheck source=tasks/scripts/gum-ui.sh
. "${repo_root}/tasks/scripts/gum-ui.sh"
dry_run="${ADAETUM_INIT_DRY_RUN:-0}"
clean_run="${ADAETUM_INIT_CLEAN:-0}"
silent_run="${ADAETUM_INIT_SILENT:-0}"
first_run="${ADAETUM_FIRST_RUN:-0}"
platform_profile="${ADAETUM_PLATFORM_PROFILE:-${repo_root}/platform.yaml}"
supports_color=0
if [ -t 1 ]; then
  supports_color=1
fi
have_openssl=0
if command -v openssl >/dev/null 2>&1; then
  have_openssl=1
fi

if [ "${supports_color}" = "1" ]; then
  c_reset="$(printf '\033[0m')"
  c_dim="$(printf '\033[2m')"
  c_bold="$(printf '\033[1m')"
  c_blue="$(printf '\033[34m')"
  c_green="$(printf '\033[32m')"
  c_yellow="$(printf '\033[33m')"
  c_red="$(printf '\033[31m')"
else
  c_reset=""
  c_dim=""
  c_bold=""
  c_blue=""
  c_green=""
  c_yellow=""
  c_red=""
fi

TOTAL_STEPS=4
current_setup_step=""
setup_local_iso_path=""
iso_preflight_announced=0
setup_detail_log="${repo_root}/.adaetum/logs/task-init-details.log"

banner() {
  if [ "${first_run}" = "1" ]; then
    adaetum_ui_hero "ADAETUM  /  FIRST-RUN" "Build your break-glass cluster" "Repository · Providers · Profile · Installer · Bootstrap"
    adaetum_ui_roadmap "1 Repository  ›  2 Providers  ›  3 Profile  ›  4 Installer  ›  5 Bootstrap"
  else
    adaetum_ui_hero "ADAETUM  /  SETUP" "Prepare your break-glass cluster" "Validate · Render · Publish · Bootstrap"
  fi
  adaetum_ui_message "${ADAETUM_UI_MUTED}" "Checkout: ${repo_root}"
  printf '\n'
}

main_step() {
  local idx="$1"
  local label="$2"
  current_setup_step="${idx}"
  if [ "${first_run}" = "1" ]; then
    adaetum_ui_milestone "5.${idx}" "${label}"
    return 0
  fi
  printf '\n'
  adaetum_ui_progress "${idx}" "${TOTAL_STEPS}" "${label}"
}

sub_step() {
  local idx="$1"
  local label="$2"
  local display_idx="${idx}"
  local suffix="${idx#*.}"
  if [ "${first_run}" = "1" ] && [ -n "${current_setup_step}" ]; then
    suffix="${idx#${current_setup_step}.}"
    display_idx="5.${current_setup_step}.${suffix}"
  fi
  if [[ "${suffix}" == *.* ]]; then
    adaetum_ui_subtask "${display_idx}" "${label}"
    return
  fi
  adaetum_ui_task "${display_idx}" "${label}"
}

ok() {
  if adaetum_gum_enabled; then adaetum_ui_status success "$1"; else printf '%s\n' "${c_green}[DONE] ${1}${c_reset}"; fi
}

warn() {
  if adaetum_gum_enabled; then adaetum_ui_status warning "$1"; else printf '%s\n' "${c_yellow}[WARN] ${1}${c_reset}"; fi
}

die() {
  if adaetum_gum_enabled; then adaetum_ui_status error "$1" >&2; else printf '%s\n' "${c_red}[ERROR] ${1}${c_reset}" >&2; fi
  exit 1
}

prompt_value() {
  local label="$1"
  local default="${2:-}"
  local secret="${3:-0}"
  local value=""
  # Non-interactive callers must supply values through the environment/cache;
  # never try to read from a closed CI or automation stdin stream.
  if [ ! -t 0 ]; then
    if [ -n "${default}" ]; then
      printf '%s' "${default}" | tr -d '\r\n'
      return 0
    fi
    echo "Missing required input for ${label} in non-interactive mode." >&2
    return 1
  fi
  if adaetum_gum_enabled; then
    # Do not pass existing secrets to Gum as a displayed default. A blank
    # password input keeps the prior value below, matching the plain prompt.
    value="$(adaetum_gum_input "${label}" "${default}" "${secret}")" || return 1
    if [ -z "${value}" ]; then
      value="${default}"
    fi
    printf '%s' "${value}" | tr -d '\r\n'
    return 0
  fi
  printf '\n'
  prompt_text="${label}${default:+ [${default}]}: "
  if [ "${secret}" = "1" ]; then
    prompt_text="${label}${default:+ [default set]}: "
  fi
  printf '%s' "${prompt_text}" >&2
  if [ "${secret}" = "1" ]; then
    if [ -r /dev/tty ]; then
      read -r -s value </dev/tty
    else
      read -r -s value
    fi
    printf '\n' >&2
  else
    if [ -r /dev/tty ]; then
      read -r value </dev/tty
    else
      read -r value
    fi
  fi
  if [ -z "${value}" ]; then
    value="${default}"
  fi
  printf '%s' "${value}" | tr -d '\r\n'
}

prepare_cloudflare_bootstrap_token() {
  # Cloudflare only displays a newly-created API token once. The handoff keeps
  # that unavoidable dashboard action immediately beside the one password
  # prompt that receives it, rather than sending an operator to documentation.
  if [ "${ADAETUM_CLOUDFLARE_AUTHORIZED:-0}" = "1" ] || { [ "${dry_run}" != "1" ] && [ -n "${default_cf_token}" ]; }; then
    return 0
  fi

  sub_step "1.1a" "Create the Cloudflare bootstrap token"
  printf '%s\n' "Adaetum will use this one token to create or reuse the R2 bucket, its scoped upload credential, Cloudflare Tunnel, and the required DNS records."
  printf '%s\n' "The token must be restricted to the Adaetum Cloudflare account and the zone for ${default_zone_input}."
  printf '%s\n' "In Manage Account > Account API Tokens, create Adaetum bootstrap with Account API Tokens Read/Write, Workers R2 Storage Read/Write, Cloudflare Tunnel Write, Connectivity Directory Read/Bind/Admin, and Workers Scripts Read/Write for the entire target account. Add Zone Read, DNS Read/Write, and Workers Routes Read/Write for the target domain only."

  if [ "${dry_run}" != "1" ] && adaetum_ui_confirm "Open Cloudflare's token page now?" "y"; then
    if ! adaetum_open_url "https://dash.cloudflare.com/profile/api-tokens"; then
      warn "Unable to open a browser automatically. Open Cloudflare My Profile > API Tokens, then follow the Account API Tokens link for the target account."
    fi
  fi

  adaetum_ui_confirm "I created the token and copied it. Continue to secure entry?" "y" || \
    die "No token was entered. Return to task init after creating the Cloudflare bootstrap token."
}

prepare_tailscale_user_token() {
  if [ "${dry_run}" != "1" ] && [ -n "${default_ts_user_token}" ]; then
    return 0
  fi
  sub_step "1.3a" "Create the Tailscale setup token"
  printf '%s\n' "Adaetum uses this token once to validate your tailnet and create the bootstrap identity. It does not store the token in platform.yaml."
  if [ "${dry_run}" != "1" ] && adaetum_ui_confirm "Open Tailscale's API key page now?" "y"; then
    adaetum_open_url "https://login.tailscale.com/admin/settings/keys" || \
      warn "Unable to open a browser automatically. Open the Tailscale admin API key page to create the token."
  fi
  adaetum_ui_confirm "I created the Tailscale API token and copied it. Continue to secure entry?" "y" || \
    die "No Tailscale token was entered. Return to task init after creating it."
}

prepare_tailscale_oauth_client() {
  if [ "${dry_run}" != "1" ] && [ -n "${default_ts_oauth_client_id}" ] && [ -n "${default_ts_oauth_client_secret}" ]; then
    return 0
  fi
  printf '%s\n' "Adaetum also needs an OAuth client for long-term node enrollment. Create one for this tailnet, allow auth-key creation, then copy its client ID and secret."
  if [ "${dry_run}" != "1" ] && adaetum_ui_confirm "Open Tailscale's OAuth client page now?" "y"; then
    adaetum_open_url "https://login.tailscale.com/admin/settings/oauth" || \
      warn "Unable to open a browser automatically. Open the Tailscale OAuth client page to create the client."
  fi
  adaetum_ui_confirm "I created the OAuth client and copied both values. Continue to secure entry?" "y" || \
    die "Tailscale OAuth credentials are required. Return to task init after creating them."
}

existing_env_value() {
  local file="$1"
  local key="$2"
  if [ "${clean_run}" = 1 ]; then
    return 0
  fi
  if [ -f "${file}" ]; then
    awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${file}" | tr -d '\r\n'
  fi
}

upsert_env_value() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp=""
  if [ "${dry_run}" = "1" ]; then
    return 0
  fi
  if [ ! -f "${file}" ]; then
    printf '%s=%s\n' "${key}" "${value}" > "${file}"
    return 0
  fi
  # Replace atomically so an interrupted setup run cannot leave a half-written
  # cache or .env file containing only part of the required secret set.
  tmp="$(mktemp)"
  awk -v k="${key}" -v v="${value}" '
    BEGIN { done=0 }
    $0 ~ ("^" k "=") { print k "=" v; done=1; next }
    { print }
    END { if (!done) print k "=" v }
  ' "${file}" > "${tmp}"
  mv "${tmp}" "${file}"
}

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

normalize_compact() {
  local value="${1:-}"
  printf '%s' "${value}" | tr -d '\r\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//'
}

encode_base64_compact() {
  local value="${1:-}"
  local py_cmd=""
  py_cmd="$(resolve_python_cmd || true)"
  if [ -n "${py_cmd}" ]; then
    "${py_cmd}" - <<'PY' "${value}"
import base64, sys
value = sys.argv[1] if len(sys.argv) > 1 else ""
print(base64.b64encode(value.encode("utf-8")).decode("ascii"), end="")
PY
    return 0
  fi
  printf '%s' "${value}" | openssl base64 -A
}

git_basic_auth_header() {
  local username="$1"
  local token="$2"
  local pair="${username}:${token}"
  printf 'AUTHORIZATION: basic %s' "$(encode_base64_compact "${pair}")"
}

generate_backup_passphrase() {
  if [ "${have_openssl}" = "1" ]; then
    openssl rand -base64 24 | tr '+/' '-_' | tr -d '=\r\n' | cut -c1-24
    return 0
  fi
  local py_cmd=""
  py_cmd="$(resolve_python_cmd || true)"
  if [ -n "${py_cmd}" ]; then
    "${py_cmd}" - <<'PY'
import base64, os
print(base64.urlsafe_b64encode(os.urandom(24)).decode("ascii").rstrip("=")[:24], end="")
PY
    return 0
  fi
  rand_token | cut -c1-24
}

backup_passphrase_fingerprint() {
  local value="${1:-}"
  local py_cmd=""
  py_cmd="$(resolve_python_cmd || true)"
  if [ -n "${py_cmd}" ]; then
    "${py_cmd}" - <<'PY' "${value}"
import hashlib
import sys
value = sys.argv[1] if len(sys.argv) > 1 else ""
print(hashlib.sha256(value.encode("utf-8")).hexdigest(), end="")
PY
    return 0
  fi
  if [ "${have_openssl}" = "1" ]; then
    printf '%s' "${value}" | openssl dgst -sha256 -r | awk '{print $1}'
    return 0
  fi
  return 1
}

has_prior_setup_state() {
  local current=""
  local keys=(
    SETUP_CLOUDFLARE_API_TOKEN
    SETUP_GITHUB_SYNC_TOKEN
    SETUP_TAILSCALE_USER_API_TOKEN
    SETUP_TAILSCALE_DOMAIN
    KS_SHARED_TOKEN
    KS_UPLOAD_TOKEN
    GITHUB_SYNC_TOKEN
    CLOUDFLARE_API_TOKEN
    TAILSCALE_USER_API_TOKEN
  )

  for current in "${keys[@]}"; do
    if [ -f ".env" ] && [ -n "$(normalize_compact "$(existing_env_value .env "${current}")")" ]; then
      return 0
    fi
  done
  return 1
}

github_token_looks_git_capable() {
  case "${1:-}" in
    github_pat_*|ghp_*|gho_*|ghu_*|ghs_*) return 0 ;;
    *) return 1 ;;
  esac
}

ensure_cached_backup_passphrase() {
  local rotate_allowed=""
  local fingerprint=""
  backup_passphrase="$(normalize_compact "${backup_passphrase}")"
  backup_passphrase_b64="$(normalize_compact "${backup_passphrase_b64}")"
  rotate_allowed="$(printf '%s' "${SETUP_ALLOW_BACKUP_PASSPHRASE_ROTATE:-0}" | tr '[:upper:]' '[:lower:]')"


  if [ -z "${backup_passphrase}" ] && [ -f ".env" ]; then
    backup_passphrase="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE)")"
  fi
  if [ -z "${backup_passphrase_b64}" ] && [ -f ".env" ]; then
    backup_passphrase_b64="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  fi

  if [ -z "${backup_passphrase}" ] && [ -n "${backup_passphrase_b64}" ]; then
    py_cmd="$(resolve_python_cmd || true)"
    if [ -n "${py_cmd}" ]; then
      backup_passphrase="$("${py_cmd}" - <<'PY' "${backup_passphrase_b64}"
import base64, sys
value = sys.argv[1] if len(sys.argv) > 1 else ""
print(base64.b64decode(value.encode("ascii")).decode("utf-8"), end="")
PY
)"
      backup_passphrase="$(normalize_compact "${backup_passphrase}")"
    fi
  fi

  if [ -z "${backup_passphrase}" ]; then
    if has_prior_setup_state && [ "${rotate_allowed}" != "1" ] && [ "${rotate_allowed}" != "true" ] && [ "${rotate_allowed}" != "yes" ]; then
      die "BOOTSTRAP_BACKUP_PASSPHRASE is missing even though prior setup state exists. Refusing to generate a new value automatically because that would orphan older emergency kits. Restore the original passphrase into .env, or rerun with SETUP_ALLOW_BACKUP_PASSPHRASE_ROTATE=1 if you are intentionally rotating it."
    fi
    backup_passphrase="$(generate_backup_passphrase)"
    fingerprint="$(backup_passphrase_fingerprint "${backup_passphrase}" || true)"
    adaetum_ui_status info "Generated a new bootstrap backup passphrase${fingerprint:+ (fingerprint ${fingerprint})}."
  fi
  if [ -z "${backup_passphrase_b64}" ] && [ -n "${backup_passphrase}" ]; then
    backup_passphrase_b64="$(encode_base64_compact "${backup_passphrase}")"
  fi
}

is_valid_http_url() {
  local value="$1"
  printf '%s' "${value}" | grep -Eq '^https?://[^[:space:]]+$'
}

is_valid_domain() {
  local value="$1"
  printf '%s' "${value}" | grep -Eq '^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'
}

resolve_task_cmd() {
  if command -v task >/dev/null 2>&1; then
    printf 'task'
    return 0
  fi
  if command -v task.exe >/dev/null 2>&1; then
    printf 'task.exe'
    return 0
  fi
  return 1
}

require_local_root_iso_for_golden_upload() {
  local local_isos=()
  local line=""
  while IFS= read -r line; do
    [ -n "${line}" ] || continue
    local_isos+=("${line}")
  done <<EOF
$(find "${repo_root}" -maxdepth 1 -type f -name '*.iso' | sort)
EOF

  if [ "${#local_isos[@]}" -eq 0 ] && [ "${dry_run}" = "1" ] && [ "${ADAETUM_INSTALLER_MEDIA_READY:-0}" = "1" ]; then
    local dry_run_arch="x86_64"
    case "$(uname -m)" in
      arm64|aarch64)
        dry_run_arch="aarch64"
        ;;
    esac
    setup_local_iso_path="${repo_root}/${ADAETUM_DRY_RUN_ISO_NAME:-Rocky-10.2-${dry_run_arch}-minimal.iso}"
    if [ "${iso_preflight_announced}" = "0" ]; then
      ok "Dry run validated the selected Rocky installer media."
      iso_preflight_announced=1
    fi
    return 0
  fi
  if [ "${#local_isos[@]}" -eq 0 ]; then
    die "Step 3.1 (Golden ISO upload) requires a local installer ISO in repo root (${repo_root}). Download the ISO, place it in repo root, then re-run setup."
  fi
  setup_local_iso_path="${local_isos[0]}"
  if [ "${iso_preflight_announced}" = "0" ]; then
    ok "Detected ${#local_isos[@]} root ISO(s) for step 3.1."
    if [ "${#local_isos[@]}" -gt 1 ]; then
      warn "Multiple root ISOs detected; early local ISO build will use: ${setup_local_iso_path}"
    fi
    iso_preflight_announced=1
  fi
}

derived_local_iso_output_path() {
  local source_iso="${1:-}"
  local iso_base=""
  if [ -z "${source_iso}" ]; then
    return 0
  fi
  iso_base="$(basename "${source_iso}")"
  printf '%s/dist/%s-ks.iso' "${repo_root}" "${iso_base%.iso}"
}

installer_download_directory() {
  # Keep the handoff predictable across macOS, Linux, and the Bash environment
  # used by Windows setup. An explicit override supports unusual home layouts
  # without adding a second destination-selection UI.
  printf '%s' "${ADAETUM_INSTALLER_DOWNLOAD_DIR:-${HOME}/Downloads}"
}

offer_installer_download() {
  local installer_iso="$1"
  local download_dir="" download_path="" partial_path=""

  download_dir="$(installer_download_directory)"
  download_path="${download_dir}/$(basename "${installer_iso}")"

  adaetum_ui_panel "Download the machine installer"
  adaetum_ui_message "${ADAETUM_UI_MUTED}" "Save a convenient copy in your Downloads folder for attaching to a physical host, VM, or remote-management console. The verified build under dist remains unchanged."
  adaetum_ui_key_value "Download location" "${download_path}"
  if ! adaetum_ui_confirm "Download the installer ISO now?" y; then
    adaetum_ui_status info "Installer remains available at ${installer_iso}."
    return 0
  fi

  if [ "${dry_run}" = "1" ]; then
    adaetum_ui_status success "Dry run would save the generated machine installer to ${download_path}."
    return 0
  fi

  if [ -f "${download_path}" ]; then
    if cmp -s "${installer_iso}" "${download_path}"; then
      adaetum_ui_status success "The current machine installer is already in Downloads."
      adaetum_ui_key_value "Downloaded ISO" "${download_path}"
      return 0
    fi
    if ! adaetum_ui_confirm "Replace the older ISO already at this download location?" n; then
      adaetum_ui_status info "Existing download left unchanged; use ${installer_iso} for this installation."
      return 0
    fi
  fi

  mkdir -p "${download_dir}" || die "Unable to create the installer download directory: ${download_dir}"
  partial_path="${download_path}.partial.$$"
  rm -f "${partial_path}"
  if ! cp "${installer_iso}" "${partial_path}"; then
    rm -f "${partial_path}"
    die "Unable to copy the installer ISO to ${download_dir}."
  fi
  if ! mv -f "${partial_path}" "${download_path}"; then
    rm -f "${partial_path}"
    die "Unable to finalize the downloaded installer ISO: ${download_path}"
  fi
  adaetum_ui_status success "Machine installer saved to Downloads."
  adaetum_ui_key_value "Downloaded ISO" "${download_path}"
}

show_installer_handoff() {
  local installer_iso=""
  installer_iso="$(derived_local_iso_output_path "${setup_local_iso_path}")"

  adaetum_ui_panel "Your installer"
  if [ "${dry_run}" = "1" ]; then
    adaetum_ui_status info "Dry run would create the automated installer shown below."
  else
    if [ ! -f "${installer_iso}" ]; then
      die "Installer handoff failed because the built ISO is missing: ${installer_iso}"
    fi
    adaetum_ui_status success "Automated Rocky Linux installer ISO ready."
  fi
  adaetum_ui_key_value "ISO file" "${installer_iso}"
  adaetum_ui_message "${ADAETUM_UI_MUTED}" "Next: attach this ISO to the target physical host or VM and boot from it once. The Rocky Linux installation runs unattended. Detach or eject the ISO when the installer reboots so the machine starts from disk. First-boot cluster preparation then continues automatically; allow roughly 30 minutes for the initial node."
  if [ "${first_run}" = "1" ]; then
    offer_installer_download "${installer_iso}"
  fi
}

assert_fresh_local_iso_output() {
  local source_iso="${1:-}"
  local output_iso=""
  local newest_input=""

  if [ -z "${source_iso}" ]; then
    die "Cannot verify local install ISO freshness: source ISO path is unknown."
  fi

  output_iso="$(derived_local_iso_output_path "${source_iso}")"
  if [ ! -f "${output_iso}" ]; then
    die "task initialize did not produce the expected local installer ISO: ${output_iso}"
  fi

  newest_input=".env"
  if [ -f "${repo_root}/pods/cluster-config/cluster-config.env" ] && [ "${repo_root}/pods/cluster-config/cluster-config.env" -nt "${newest_input}" ]; then
    newest_input="${repo_root}/pods/cluster-config/cluster-config.env"
  fi
  if [ -f "${repo_root}/platform.yaml" ] && [ "${repo_root}/platform.yaml" -nt "${newest_input}" ]; then
    newest_input="${repo_root}/platform.yaml"
  fi
  if [ -f "${repo_root}/dist/ks-templates/rocky10.ks" ] && [ "${repo_root}/dist/ks-templates/rocky10.ks" -nt "${newest_input}" ]; then
    newest_input="${repo_root}/dist/ks-templates/rocky10.ks"
  fi

  if [ "${output_iso}" -ot "${newest_input}" ]; then
    die "Local installer ISO is stale: ${output_iso} is older than ${newest_input}. Re-run task initialize after fixing the local ISO build."
  fi

  ok "Fresh local installer ISO ready: ${output_iso}"
}

task_cmd="$(resolve_task_cmd || true)"
if [ -z "${task_cmd}" ]; then
  die "Missing required command: task"
fi

if [ "${first_run}" = "1" ]; then
  # shellcheck source=tasks/scripts/first-run-preflight.sh
  . "${repo_root}/tasks/scripts/first-run-preflight.sh"
fi

banner

if [ "${first_run}" = "1" ]; then
  if [ "${clean_run}" = 1 ]; then
    adaetum_ui_panel "Clean initialization"
    adaetum_ui_message "${ADAETUM_UI_MUTED}" "Saved Adaetum provider credentials and prior runtime values will be ignored. Newly validated values will replace them. The GitHub login, private recovery repository, and verified installer media remain reusable."
  fi
  adaetum_first_run_prepare
  # Dry-run first-run preparation creates a temporary reviewed profile. Resolve
  # the profile only after that shared preparation has completed so every later
  # step derives its values from the same reviewed configuration.
  platform_profile="${ADAETUM_PLATFORM_PROFILE:-${platform_profile}}"
fi

default_cf_token="${SETUP_CLOUDFLARE_API_TOKEN:-}"
default_cf_account_id="${SETUP_CLOUDFLARE_ACCOUNT_ID:-}"
default_gh_token="${SETUP_GITHUB_SYNC_TOKEN:-}"
default_ts_user_token="${SETUP_TAILSCALE_USER_API_TOKEN:-}"
default_ts_domain="${SETUP_TAILSCALE_DOMAIN:-}"
default_ts_oauth_client_id="${SETUP_TAILSCALE_OAUTH_CLIENT_ID:-}"
default_ts_oauth_client_secret="${SETUP_TAILSCALE_OAUTH_CLIENT_SECRET:-}"
default_backup_passphrase="${SETUP_BOOTSTRAP_BACKUP_PASSPHRASE:-}"
default_backup_passphrase_b64="${SETUP_BOOTSTRAP_BACKUP_PASSPHRASE_B64:-}"
default_zone_input="${SETUP_ZONE_INPUT:-example.services}"
if [ "${dry_run}" = "1" ]; then
  # Fixtures keep the existing hidden prompts and input checks usable without
  # asking an operator to paste credentials into a no-mutation rehearsal.
  default_cf_token="${default_cf_token:-dry-run-cloudflare-token}"
  default_gh_token="${default_gh_token:-ghp_dryrunplaceholdertoken0000000000000000}"
  default_ts_user_token="${default_ts_user_token:-tskey-api-dry-run-placeholder}"
  default_ts_oauth_client_id="${default_ts_oauth_client_id:-dry-run-client-id}"
  default_ts_oauth_client_secret="${default_ts_oauth_client_secret:-dry-run-client-secret}"
fi
default_cf_token="$(normalize_compact "${default_cf_token}")"
default_cf_account_id="$(normalize_compact "${default_cf_account_id}")"
default_gh_token="$(normalize_compact "${default_gh_token}")"
default_ts_user_token="$(normalize_compact "${default_ts_user_token}")"
default_ts_domain="$(normalize_compact "${default_ts_domain}")"
default_ts_oauth_client_id="$(normalize_compact "${default_ts_oauth_client_id}")"
default_ts_oauth_client_secret="$(normalize_compact "${default_ts_oauth_client_secret}")"
if [ -z "${default_ts_oauth_client_id}" ]; then
  default_ts_oauth_client_id="$(normalize_compact "$(existing_env_value .env TAILSCALE_OAUTH_CLIENT_ID)")"
fi
if [ -z "${default_ts_oauth_client_secret}" ]; then
  default_ts_oauth_client_secret="$(normalize_compact "$(existing_env_value .env TAILSCALE_OAUTH_CLIENT_SECRET)")"
fi
default_zone_input="$(normalize_compact "${default_zone_input}")"
py_cmd="$(resolve_python_cmd || true)"
if [ -z "${py_cmd}" ]; then
  die "Missing required command: python3 for platform profile rendering."
fi
profile_env_file="$(mktemp)"
if ! "${py_cmd}" ./tasks/scripts/validate-platform-profile.py --quiet --profile "${platform_profile}"; then
  rm -f "${profile_env_file}"
  exit 1
fi
if ! "${py_cmd}" ./tasks/scripts/render-platform-profile.py --profile "${platform_profile}" --output-setup-env "${profile_env_file}"; then
  rm -f "${profile_env_file}"
  exit 1
fi
profile_value() {
  local key="$1"
  awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${profile_env_file}" | tr -d '\r\n'
}
default_zone_input="$(profile_value CLUSTER_DOMAIN)"
default_ts_domain="$(profile_value TAILSCALE_DOMAIN)"
profile_cluster_tag="$(profile_value TAILSCALE_CLUSTER_TAG)"
profile_ks_base_url="$(profile_value KS_BASE_URL)"
profile_r2_bucket="$(profile_value R2_BUCKET)"
rm -f "${profile_env_file}"
unset -f profile_value
if [ -n "${ADAETUM_PLATFORM_PROFILE_TEMP:-}" ] && [ "${platform_profile}" = "${ADAETUM_PLATFORM_PROFILE_TEMP}" ]; then
  ok "Reviewed first-run platform profile validated."
else
  ok "Platform profile validated: ${platform_profile}."
fi
# Initialize early so set -u does not fail before OAuth prompts run.
ts_oauth_client_id="${default_ts_oauth_client_id}"
ts_oauth_client_secret="${default_ts_oauth_client_secret}"
selected_step="${SETUP_STEP:-${SETUP_ONLY_STEP:-}}"
selected_substep="${SETUP_SUBSTEP:-}"
if [ -n "${selected_step}" ]; then
  case "${selected_step}" in
    1|2|3|4) ;;
    *) die "Invalid SETUP_STEP value: ${selected_step}. Use 1, 2, 3, or 4." ;;
  esac
  warn "Running only setup step ${selected_step} (SETUP_STEP=${selected_step})."
fi
if [ -n "${selected_substep}" ]; then
  case "${selected_substep}" in
    2.2) ;;
    *) die "Invalid SETUP_SUBSTEP value: ${selected_substep}. Supported value: 2.2" ;;
  esac
  if [ -n "${selected_step}" ]; then
    die "Use either SETUP_STEP or SETUP_SUBSTEP, not both."
  fi
  warn "Running only setup substep ${selected_substep} (SETUP_SUBSTEP=${selected_substep})."
fi

step_enabled() {
  local idx="$1"
  if [ -z "${selected_step}" ] || [ "${selected_step}" = "${idx}" ]; then
    return 0
  fi
  return 1
}

gitops_rendered_subset_pathspecs() {
  cat <<'EOF'
pods/cluster-config/cluster-config.env
platform.yaml
pods/argocd/bootstrap/app-of-apps.yaml
pods/argocd/bootstrap/applicationset.yaml
pods/argocd/platform/pre-openbao/openbao.yaml
pods/argocd/platform/post-openbao/application.yaml
pods/argocd/platform/post-openbao/openbao-config.yaml
pods/gitea/gitea-values.yaml
pods/ansible/ansible/ansible-runner-deployment.yaml
pods/ansible/ansible/ansible-cluster-config.yaml
pods/ingress/external-dns/deployment.yaml
pods/ingress/ingress-routing.app.yaml
pods/ingress/ingress-cluster-config.yaml
pods/ingress/nginx-routing/argocd-ingress.yaml
pods/ingress/nginx-routing/argocd-public-ingress.yaml
pods/ingress/nginx-routing/authentik-outpost-hosts-ingress.yaml
pods/ingress/nginx-routing/authentik-ingress.yaml
pods/ingress/nginx-routing/gitea-ingress.yaml
pods/ingress/nginx-routing/gitea-public-ingress.yaml
pods/ingress/nginx-routing/headlamp-ingress.yaml
pods/ingress/nginx-routing/headlamp-public-ingress.yaml
pods/ingress/nginx-routing/homepage-ingress.yaml
pods/ingress/nginx-routing/homepage-public-ingress.yaml
pods/ingress/nginx-routing/openbao-ingress.yaml
pods/ingress/nginx-routing/openbao-public-ingress.yaml
pods/ingress/nginx-routing/registry-ingress.yaml
pods/ingress/nginx-routing/registry-public-ingress.yaml
pods/ingress/observability-routing/observability-routing-cluster-config.yaml
pods/ingress/observability-routing/alertmanager-ingress.yaml
pods/ingress/observability-routing/alertmanager-public-ingress.yaml
pods/ingress/observability-routing/grafana-ingress.yaml
pods/ingress/observability-routing/grafana-public-ingress.yaml
pods/ingress/observability-routing/prometheus-ingress.yaml
pods/ingress/observability-routing/prometheus-public-ingress.yaml
pods/portal/homepage/homepage-cluster-config.yaml
EOF
}

current_git_branch() {
  local branch=""
  branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [ -z "${branch}" ] || [ "${branch}" = "HEAD" ]; then
    return 1
  fi
  printf '%s' "${branch}"
}

infer_github_repo() {
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

infer_github_username() {
  local repo_full_name="${1:-}"
  if [ -n "${ARGOCD_GITHUB_USERNAME:-}" ]; then
    printf '%s' "${ARGOCD_GITHUB_USERNAME}"
    return 0
  fi
  if [ -n "${GITEA_SEED_SOURCE_USERNAME:-}" ]; then
    printf '%s' "${GITEA_SEED_SOURCE_USERNAME}"
    return 0
  fi
  if [ -n "${GITEA_PUSH_MIRROR_USERNAME:-}" ]; then
    printf '%s' "${GITEA_PUSH_MIRROR_USERNAME}"
    return 0
  fi
  if [ -n "${repo_full_name}" ] && printf '%s' "${repo_full_name}" | grep -q '/'; then
    printf '%s' "${repo_full_name%%/*}"
    return 0
  fi
  return 1
}

prepare_gitops_push_context() {
  if ! command -v git >/dev/null 2>&1; then
    die "Missing required command: git"
  fi
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    die "task initialize must run inside a git work tree."
  fi
  if ! github_token_looks_git_capable "${gh_token}"; then
    die "GITHUB_SYNC_TOKEN must be a git-capable GitHub token to push rendered GitOps state."
  fi

  gitops_branch="$(current_git_branch || true)"
  if [ -z "${gitops_branch}" ]; then
    die "task initialize requires a checked-out branch. Detached HEAD is not supported for rendered GitOps pushes."
  fi

  gitops_repo="${GH_REPO:-}"
  if [ -z "${gitops_repo}" ]; then
    gitops_repo="$(infer_github_repo || true)"
  fi
  if [ -z "${gitops_repo}" ]; then
    die "Unable to determine the GitHub repo for rendered GitOps push. Set GH_REPO=owner/repo or configure origin."
  fi

  gitops_username="$(infer_github_username "${gitops_repo}" || true)"
  if [ -z "${gitops_username}" ]; then
    die "Unable to determine the GitHub username for rendered GitOps push. Set ARGOCD_GITHUB_USERNAME or GITEA_SEED_SOURCE_USERNAME."
  fi

  gitops_push_url="https://github.com/${gitops_repo}.git"
}

gitops_subset_has_changes() {
  local -a pathspecs=("$@")
  if ! git diff --quiet --exit-code -- "${pathspecs[@]}"; then
    return 0
  fi
  if ! git diff --cached --quiet --exit-code -- "${pathspecs[@]}"; then
    return 0
  fi
  return 1
}

verify_remote_gitops_head() {
  local push_url="$1"
  local branch="$2"
  local local_head=""
  local remote_head=""
  local auth_header=""

  local_head="$(git rev-parse HEAD 2>/dev/null || true)"
  if [ -z "${local_head}" ]; then
    die "Unable to determine local HEAD for rendered GitOps verification."
  fi

  auth_header="$(git_basic_auth_header "${gitops_username}" "${gh_token}")"
  remote_head="$(
    GIT_TERMINAL_PROMPT=0 \
      git -c credential.helper= \
          -c "http.${push_url}/.extraheader=${auth_header}" \
          ls-remote "${push_url}" "refs/heads/${branch}" 2>/dev/null | awk 'NR==1 {print $1}'
  )"
  if [ -z "${remote_head}" ]; then
    die "Rendered GitOps push verification failed: could not read remote branch ${branch} from ${push_url}."
  fi
  if [ "${remote_head}" != "${local_head}" ]; then
    die "Rendered GitOps push verification failed: remote ${branch} is ${remote_head}, local HEAD is ${local_head}."
  fi
}

commit_and_push_rendered_gitops_state() {
  local -a pathspecs=()
  local path=""
  local commit_created=0
  local auth_header=""

  prepare_gitops_push_context

  while IFS= read -r path; do
    [ -n "${path}" ] || continue
    pathspecs+=("${path}")
  done <<EOF
$(gitops_rendered_subset_pathspecs)
EOF

  if [ "${#pathspecs[@]}" -eq 0 ]; then
    die "Rendered GitOps subset is empty; cannot continue."
  fi

  if gitops_subset_has_changes "${pathspecs[@]}"; then
    git config user.name >/dev/null 2>&1 || git config user.name "initialize"
    git config user.email >/dev/null 2>&1 || git config user.email "initialize@local"
    git commit --only -m "Render cluster GitOps state" -- "${pathspecs[@]}" >/dev/null 2>&1 || {
      die "Failed to create the rendered GitOps commit. Resolve git conflicts or staged-path issues and rerun task initialize."
    }
    commit_created=1
    ok "Rendered GitOps subset committed."
  fi

  auth_header="$(git_basic_auth_header "${gitops_username}" "${gh_token}")"
  GIT_TERMINAL_PROMPT=0 \
    git -c credential.helper= \
        -c "http.${gitops_push_url}/.extraheader=${auth_header}" \
        push "${gitops_push_url}" "HEAD:${gitops_branch}" >/dev/null 2>&1 || \
    die "Failed to push rendered GitOps state to ${gitops_repo} on branch ${gitops_branch}. Check GITHUB_SYNC_TOKEN and branch push permissions."

  verify_remote_gitops_head "${gitops_push_url}" "${gitops_branch}"

  if [ "${commit_created}" = "1" ]; then
    ok "Rendered GitOps state pushed to GitHub."
  else
    ok "Rendered GitOps state already committed; branch push verified."
  fi
}

cf_token="${default_cf_token}"
gh_token="${default_gh_token}"
ts_user_token="${default_ts_user_token}"
ts_domain="${default_ts_domain}"
zone_input="${default_zone_input}"
ks_base_url=""
early_local_iso_pid=""
early_local_iso_log=""
tmp_existing=""
gitops_branch=""
gitops_repo=""
gitops_username=""
gitops_push_url=""
backup_passphrase="${default_backup_passphrase}"
backup_passphrase_b64="${default_backup_passphrase_b64}"

cleanup() {
  if [ -n "${tmp_existing}" ]; then
    rm -f "${tmp_existing}"
  fi
  if [ -n "${ADAETUM_PLATFORM_PROFILE_TEMP:-}" ]; then
    rm -f "${ADAETUM_PLATFORM_PROFILE_TEMP}"
  fi
}
trap cleanup EXIT

normalize_and_compute_ks_base() {
  cf_token="$(normalize_compact "${cf_token}")"
  gh_token="$(normalize_compact "${gh_token}")"
  ts_user_token="$(normalize_compact "${ts_user_token}")"
  ts_domain="$(normalize_compact "${ts_domain}")"
  zone_input="$(normalize_compact "${zone_input}")"

  if [ -n "${profile_ks_base_url:-}" ]; then
    ks_base_url="${profile_ks_base_url}"
    return 0
  fi

  zone_input="$(printf '%s' "${zone_input}" | tr '[:upper:]' '[:lower:]' | sed -E 's#[[:space:]]+##g; s#/$##')"
  if is_valid_http_url "${zone_input}"; then
    ks_base_url="${zone_input}"
  else
    zone_domain="$(printf '%s' "${zone_input}" | sed -E 's#^https?://##')"
    if is_valid_domain "${zone_domain}"; then
      ks_base_url="https://bootstrap.${zone_domain}"
    else
      die "Invalid zone/domain input: ${zone_input}. Enter a domain like example.services, or a full URL."
    fi
  fi
}

validate_required_inputs() {
  local require_gh="$1"
  if [ -z "${cf_token}" ]; then
    die "CLOUDFLARE_API_TOKEN is required."
  fi
  if [ "${require_gh}" = "1" ] && [ -z "${gh_token}" ]; then
    die "GITHUB_SYNC_TOKEN is required."
  fi
  if [ -z "${ts_domain}" ]; then
    die "TAILSCALE_DOMAIN is required."
  fi
  if [ -z "${ts_user_token}" ]; then
    die "TAILSCALE_USER_API_TOKEN is required for bootstrap."
  fi
}

ensure_tailscale_oauth_ready() {
  local substep_capture="$1"
  local substep_validate="$2"
  local cluster_tag=""
  local ts_bootstrap_err=""
  local ts_bootstrap_out=""
  local py_cmd=""
  local key=""
  local value=""
  local out_cluster_tag=""
  local out_advertise_tags=""
  local out_authkey=""

  sub_step "${substep_capture}" "Tailscale OAuth credentials"
  if [ -n "${default_ts_oauth_client_id}" ] && [ -n "${default_ts_oauth_client_secret}" ]; then
    ts_oauth_client_id="${default_ts_oauth_client_id}"
    ts_oauth_client_secret="${default_ts_oauth_client_secret}"
    adaetum_ui_status info "Using the OAuth client captured during provider setup."
  else
    prepare_tailscale_oauth_client
    ts_oauth_client_id="$(prompt_value "Tailscale OAuth client ID (TAILSCALE_OAUTH_CLIENT_ID)" "${default_ts_oauth_client_id}" 0)"
    ts_oauth_client_secret="$(prompt_value "Tailscale OAuth client secret (TAILSCALE_OAUTH_CLIENT_SECRET)" "${default_ts_oauth_client_secret}" 1)"
  fi
  ts_oauth_client_id="$(normalize_compact "${ts_oauth_client_id}")"
  ts_oauth_client_secret="$(normalize_compact "${ts_oauth_client_secret}")"
  if [ -z "${ts_oauth_client_id}" ] || [ -z "${ts_oauth_client_secret}" ]; then
    die "TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET are required."
  fi

  sub_step "${substep_validate}" "Validate OAuth credentials before .env/ISO render"
  if [ "${dry_run}" = "1" ]; then
    # The provider request is the action boundary; prompts above remain shared.
    ok "Stored and validated Tailscale OAuth credentials."
    return 0
  fi
  cluster_tag="$(normalize_compact "${profile_cluster_tag:-}")"
  if [ -z "${cluster_tag}" ]; then
    cluster_tag="tag:cluster"
  fi
  ts_bootstrap_err="$(mktemp)"
  if command -v uv >/dev/null 2>&1; then
    ts_bootstrap_out="$(uv run --with certifi python ./tasks/scripts/bootstrap-tailscale.py \
      --user-token "${ts_user_token}" \
      --tailnet "${ts_domain}" \
      --oauth-client-id "${ts_oauth_client_id}" \
      --oauth-client-secret "${ts_oauth_client_secret}" \
      --cluster-tag "${cluster_tag}" 2>"${ts_bootstrap_err}" || true)"
  else
    py_cmd="$(resolve_python_cmd || true)"
    if [ -n "${py_cmd}" ]; then
      ts_bootstrap_out="$("${py_cmd}" ./tasks/scripts/bootstrap-tailscale.py \
        --user-token "${ts_user_token}" \
        --tailnet "${ts_domain}" \
        --oauth-client-id "${ts_oauth_client_id}" \
        --oauth-client-secret "${ts_oauth_client_secret}" \
        --cluster-tag "${cluster_tag}" 2>"${ts_bootstrap_err}" || true)"
    fi
  fi
  if [ -z "${ts_bootstrap_out}" ]; then
    echo "OAuth validation failed:"
    cat "${ts_bootstrap_err}" || true
    rm -f "${ts_bootstrap_err}"
    die "Unable to validate provided Tailscale OAuth credentials."
  fi
  rm -f "${ts_bootstrap_err}"

  while IFS='=' read -r key value; do
    [ -n "${key}" ] || continue
    case "${key}" in
      TAILSCALE_CLUSTER_TAG) out_cluster_tag="${value}" ;;
      TAILSCALE_ADVERTISE_TAGS) out_advertise_tags="${value}" ;;
      TAILSCALE_AUTHKEY) out_authkey="${value}" ;;
    esac
  done <<< "${ts_bootstrap_out}"

  upsert_env_value .env TAILSCALE_OAUTH_CLIENT_ID "${ts_oauth_client_id}"
  upsert_env_value .env TAILSCALE_OAUTH_CLIENT_SECRET "${ts_oauth_client_secret}"
  if [ -n "${out_cluster_tag}" ]; then
    upsert_env_value .env TAILSCALE_CLUSTER_TAG "${out_cluster_tag}"
  fi
  if [ -n "${out_advertise_tags}" ]; then
    upsert_env_value .env TAILSCALE_ADVERTISE_TAGS "${out_advertise_tags}"
  fi
  if [ -n "${out_authkey}" ]; then
    upsert_env_value .env TAILSCALE_AUTHKEY "${out_authkey}"
  fi
  ok "Stored and validated Tailscale OAuth credentials."
}

if [ "${selected_substep}" = "2.2" ]; then
  main_step "2" "Build local installer"
  sub_step "2.2" "Build local install ISO only"
  require_local_root_iso_for_golden_upload
  iso_path_for_task="${setup_local_iso_path}"
  case "${iso_path_for_task}" in
    "${repo_root}/"*) iso_path_for_task="${iso_path_for_task#${repo_root}/}" ;;
  esac
  echo "    ISO_PATH=${iso_path_for_task}"
  ISO_PATH="${iso_path_for_task}" "${task_cmd}" build-iso
  ok "Substep 2.2 complete."
  exit 0
fi

if step_enabled "1"; then
  main_step "1" "Validate captured setup inputs"
  sub_step "1.0" "Validate selected installer media"
  require_local_root_iso_for_golden_upload
  prepare_cloudflare_bootstrap_token
  sub_step "1.1" "Cloudflare bootstrap token"
  if [ -n "${default_cf_token}" ]; then
    cf_token="${default_cf_token}"
    adaetum_ui_status info "Using the Cloudflare token authorized during provider setup."
  else
    cf_token="$(prompt_value "Cloudflare bootstrap token (CLOUDFLARE_API_TOKEN)" "${default_cf_token}" 1)"
  fi
  sub_step "1.2" "GitHub setup token"
  if [ -n "${default_gh_token}" ]; then
    gh_token="${default_gh_token}"
    adaetum_ui_status info "Using the GitHub credential authorized during repository setup."
  else
    gh_token="$(prompt_value "GitHub setup token (GITHUB_SYNC_TOKEN, repo-capable and mirror-write-capable)" "${default_gh_token}" 1)"
  fi
  sub_step "1.3" "Tailscale user API token"
  if [ -n "${default_ts_user_token}" ]; then
    ts_user_token="${default_ts_user_token}"
    adaetum_ui_status info "Using the Tailscale access token authorized during provider setup."
  else
    prepare_tailscale_user_token
    ts_user_token="$(prompt_value "Tailscale user token (TAILSCALE_USER_API_TOKEN)" "${default_ts_user_token}" 1)"
  fi
  sub_step "1.4" "Tailnet DNS name"
  ts_domain="${default_ts_domain}"
  adaetum_ui_status info "Using tailnet ${ts_domain} from the reviewed platform profile."
  sub_step "1.5" "Zone domain / KS base URL"
  zone_input="${default_zone_input}"
  adaetum_ui_status info "Using cluster domain ${zone_input} from the reviewed platform profile."
  normalize_and_compute_ks_base
  validate_required_inputs "1"
  if [ -n "${selected_step}" ]; then
    ok "Step 1 complete."
    exit 0
  fi
fi

if step_enabled "2"; then
  main_step "2" "Render and publish configuration"
  normalize_and_compute_ks_base
  validate_required_inputs "0"
  ensure_tailscale_oauth_ready "2.1" "2.2"
  ensure_cached_backup_passphrase
  if [ "${dry_run}" = "1" ]; then
    sub_step "2.3" "Render environment values"
    ok "Environment file generated."
    sub_step "2.4" "Sync pods cluster config"
    sub_step "2.4.1" "Render recovery-repository-owned platform.yaml"
    ok "Pods cluster config rendered."
    ok "Generated config is free of example placeholders."
    ok "Opinionated GitHub token contract is locally valid."
    ok "Rendered ingress contract is locally valid."
    ok "Bootstrap runtime payload is locally valid."
    sub_step "2.5" "Commit and push rendered GitOps state"
    ok "Rendered GitOps state pushed to GitHub."
    if [ -n "${selected_step}" ]; then
      ok "Step 2 complete."
      exit 0
    fi
  else

  tmp_existing="$(mktemp)"
  existing_ks_shared_token="$(normalize_compact "$(existing_env_value .env KS_SHARED_TOKEN)")"
  existing_ks_upload_token="$(normalize_compact "$(existing_env_value .env KS_UPLOAD_TOKEN)")"
  existing_backup_passphrase="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE)")"
  existing_backup_passphrase_b64="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  if [ -z "${existing_backup_passphrase}" ]; then
    existing_backup_passphrase="$(normalize_compact "${backup_passphrase}")"
  fi
  if [ -z "${existing_backup_passphrase_b64}" ]; then
    existing_backup_passphrase_b64="$(normalize_compact "${backup_passphrase_b64}")"
  fi
  explicit_argocd_github_token=""
  explicit_seed_source_token=""
  recovery_repo="$(infer_github_repo || true)"
  recovery_branch="$(current_git_branch || true)"
  [ -n "${recovery_repo}" ] || die "Unable to determine the private recovery repository from origin."
  [ -n "${recovery_branch}" ] || die "Unable to determine the recovery repository branch."
  recovery_repo_url="https://github.com/${recovery_repo}.git"
  explicit_argocd_github_username="$(normalize_compact "$(existing_env_value .env ARGOCD_GITHUB_USERNAME)")"
  explicit_seed_source_username="$(normalize_compact "$(existing_env_value .env GITEA_SEED_SOURCE_USERNAME)")"
  if github_token_looks_git_capable "${gh_token}"; then
    explicit_argocd_github_token="${gh_token}"
    explicit_seed_source_token="${gh_token}"
  fi
  cat > "${tmp_existing}" <<EOF
CLOUDFLARE_API_TOKEN=${cf_token}
CLOUDFLARE_ACCOUNT_ID=${default_cf_account_id}
TAILSCALE_USER_API_TOKEN=${ts_user_token}
TAILSCALE_DOMAIN=${ts_domain}
TAILSCALE_OAUTH_CLIENT_ID=${ts_oauth_client_id}
TAILSCALE_OAUTH_CLIENT_SECRET=${ts_oauth_client_secret}
KS_BASE_URL=${ks_base_url}
GITHUB_SYNC_TOKEN=${gh_token}
ARGOCD_GITHUB_REPO_URL=${recovery_repo_url}
ARGOCD_GITHUB_REPO_BRANCH=${recovery_branch}
ARGOCD_GITHUB_USERNAME=${explicit_argocd_github_username}
ARGOCD_GITHUB_TOKEN=${explicit_argocd_github_token}
GITEA_SEED_SOURCE_REPO_URL=${recovery_repo_url}
GITEA_SEED_SOURCE_REPO_BRANCH=${recovery_branch}
GITEA_SEED_SOURCE_USERNAME=${explicit_seed_source_username}
GITEA_SEED_SOURCE_TOKEN=${explicit_seed_source_token}
R2_BUCKET=${profile_r2_bucket}
KS_SHARED_TOKEN=${existing_ks_shared_token}
KS_UPLOAD_TOKEN=${existing_ks_upload_token}
BOOTSTRAP_BACKUP_PASSPHRASE=${existing_backup_passphrase}
BOOTSTRAP_BACKUP_PASSPHRASE_B64=${existing_backup_passphrase_b64}
EOF

  sub_step "2.3" "Render environment values"
  setup_detail_log="${repo_root}/.adaetum/logs/task-init-details.log"
  setup_iso_for_env="${setup_local_iso_path}"
  case "${setup_iso_for_env}" in
    "${repo_root}/"*) setup_iso_for_env="${setup_iso_for_env#${repo_root}/}" ;;
  esac
  mkdir -p "$(dirname "${setup_detail_log}")"
  : > "${setup_detail_log}"
  if ! ADAETUM_LOCAL_ISO_PATH="${setup_iso_for_env}" \
    WRITE_VM_ENV=0 \
    NON_INTERACTIVE=1 \
    OPINIONATED_GITHUB_TOKEN_MODE=1 \
    REQUIRE_CLOUDFLARE_BOOTSTRAP=1 \
    REQUIRE_TAILSCALE_BOOTSTRAP=1 \
    TAILSCALE_BOOTSTRAP_VALIDATED=1 \
    GITHUB_SYNC=1 \
    GITHUB_SYNC_REQUIRED=1 \
    GITHUB_ENVIRONMENT="${GITHUB_ENVIRONMENT:-Prod}" \
    ADAETUM_IGNORE_EXISTING_ENV="${clean_run}" \
    bash ./tasks/scripts/generate-env-files.sh .env "${tmp_existing}" >>"${setup_detail_log}" 2>&1; then
    die "Environment rendering failed. Details: ${setup_detail_log}"
  fi
  bash ./tasks/scripts/validate-opinionated-github-token-contract.sh .env >>"${setup_detail_log}" 2>&1 || \
    die "Rendered GitHub credentials are invalid. Details: ${setup_detail_log}"
  rendered_backup_passphrase="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE)")"
  rendered_backup_passphrase_b64="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  if [ -z "${rendered_backup_passphrase}" ] || [ -z "${rendered_backup_passphrase_b64}" ]; then
    die "Environment rendering omitted the bootstrap backup passphrase; setup stopped before GitOps publication."
  fi
  ok "Required setup credentials were synced to GitHub secrets."
  backup_passphrase="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE)")"
  backup_passphrase_b64="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  if [ -z "${backup_passphrase}" ] || [ -z "${backup_passphrase_b64}" ]; then
    ensure_cached_backup_passphrase
    upsert_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE "${backup_passphrase}"
    upsert_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64 "${backup_passphrase_b64}"
  fi
  ok "Environment file generated."

  sub_step "2.4" "Sync pods cluster config"
  py_cmd="$(resolve_python_cmd || true)"
  if [ -z "${py_cmd}" ]; then
    die "Missing required command: python3"
  fi
  sub_step "2.4.1" "Render recovery-repository-owned platform.yaml"
  "${py_cmd}" ./tasks/scripts/render-platform-profile.py \
    --profile ./platform.yaml \
    --runtime-env .env \
    --output-env ./dist/platform.env \
    --config-file ./pods/cluster-config/cluster-config.env \
    --render-pods >>"${setup_detail_log}" 2>&1
  "${py_cmd}" ./tasks/scripts/render-pods-config.py --check >>"${setup_detail_log}" 2>&1
  "${py_cmd}" ./.validator/validate-pods-consistency.py >>"${setup_detail_log}" 2>&1
  ok "Pods cluster config rendered."
  "${py_cmd}" ./.validator/validate-no-example-placeholders.py >>"${setup_detail_log}" 2>&1
  ok "Generated config is free of example placeholders."
  ok "Opinionated GitHub token contract is locally valid."
  "${py_cmd}" ./.validator/validate-ingress-contract.py >>"${setup_detail_log}" 2>&1
  ok "Rendered ingress contract is locally valid."
  bash ./tasks/scripts/validate-bootstrap-runtime-env.sh >>"${setup_detail_log}" 2>&1
  ok "Bootstrap runtime payload is locally valid."
  sub_step "2.5" "Commit and push rendered GitOps state"
  commit_and_push_rendered_gitops_state

  if [ -z "${selected_step}" ]; then
    early_local_iso_setting="$(printf '%s' "${INITIAL_SETUP_EARLY_LOCAL_ISO_BUILD:-0}" | tr '[:upper:]' '[:lower:]')"
    run_local_iso_setting="$(printf '%s' "${INITIAL_SETUP_RUN_LOCAL_ISO_BUILD:-1}" | tr '[:upper:]' '[:lower:]')"
    if [ "${early_local_iso_setting}" = "1" ] || [ "${early_local_iso_setting}" = "true" ] || [ "${early_local_iso_setting}" = "yes" ]; then
      if [ "${run_local_iso_setting}" != "0" ] && [ "${run_local_iso_setting}" != "false" ] && [ "${run_local_iso_setting}" != "no" ]; then
        sub_step "2.5" "Start local ISO build in background"
        require_local_root_iso_for_golden_upload
        iso_path_for_task="${setup_local_iso_path}"
        case "${iso_path_for_task}" in
          "${repo_root}/"*) iso_path_for_task="${iso_path_for_task#${repo_root}/}" ;;
        esac
        early_local_iso_log="$(mktemp "${TMPDIR:-/tmp}/local-iso-build-early.XXXXXX")"
        early_local_iso_log="${early_local_iso_log}.log"
        echo "    local ISO build log: ${early_local_iso_log}"
        (ISO_PATH="${iso_path_for_task}" "${task_cmd}" build-iso >"${early_local_iso_log}" 2>&1) &
        early_local_iso_pid="$!"
        # Prevent duplicate local ISO build inside the wizard.
        export INITIAL_SETUP_RUN_LOCAL_ISO_BUILD=0
      fi
    fi
  fi

  if [ -n "${selected_step}" ]; then
    ok "Step 2 complete."
    exit 0
  fi
  fi
fi

wizard_rc=0
if step_enabled "3"; then
  main_step "3" "Publish bootstrap artifacts"
  normalize_and_compute_ks_base
  validate_required_inputs "1"
  export GITHUB_SYNC_TOKEN="${gh_token}"
  export GITHUB_ENVIRONMENT="${GITHUB_ENVIRONMENT:-Prod}"
  export INITIAL_SETUP_AUTO_YES=1
  export INITIAL_SETUP_SKIP_ENV_SETUP=1
  export INITIAL_SETUP_EMBEDDED=1
  export INITIAL_SETUP_COMPACT=1
  export INITIAL_SETUP_DETAIL_LOG="${setup_detail_log}"
  if [ "${first_run}" = "1" ]; then
    export INITIAL_SETUP_EMBEDDED_PREFIX=5.3
  else
    export INITIAL_SETUP_EMBEDDED_PREFIX=3
  fi
  export INITIAL_SETUP_WORKFLOW_WAIT="${INITIAL_SETUP_WORKFLOW_WAIT:-0}"
  export INITIAL_SETUP_RUN_LOCAL_ISO_BUILD=1
  export INITIAL_SETUP_EARLY_LOCAL_ISO_BUILD=0
  if bash ./tasks/scripts/run-initial-setup.sh; then
    wizard_rc=0
  else
    wizard_rc=$?
  fi
  if [ "${wizard_rc}" -ne 0 ]; then
    die "Setup failed: bootstrap automation flow exited with status ${wizard_rc}. Details: ${setup_detail_log}"
  fi
  require_local_root_iso_for_golden_upload
  if [ "${dry_run}" = "1" ]; then
    if [ -n "${selected_step}" ]; then
      ok "Step 3 complete."
      exit 0
    fi
  else
  backup_passphrase="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE)")"
  backup_passphrase_b64="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  "${py_cmd}" ./.validator/validate-no-example-placeholders.py
  ok "Rendered bootstrap outputs are free of example placeholders."
  "${py_cmd}" ./.validator/validate-ingress-contract.py
  ok "Rendered ingress contract is locally valid."
  bash ./tasks/scripts/validate-bootstrap-runtime-env.sh
  ok "Bootstrap runtime payload is locally valid."
  assert_fresh_local_iso_output "${setup_local_iso_path}"
  if [ -n "${selected_step}" ]; then
    ok "Step 3 complete."
    exit 0
  fi
  fi
fi

if step_enabled "4"; then
  main_step "4" "Finalize installer"
  normalize_and_compute_ks_base
  validate_required_inputs "0"
  sub_step "4.1" "Finalize local installer"
  if [ -z "${setup_local_iso_path}" ]; then
    require_local_root_iso_for_golden_upload
  fi
  if [ -n "${early_local_iso_pid}" ]; then
    if wait "${early_local_iso_pid}"; then
      ok "Local ISO build completed successfully."
      rm -f "${early_local_iso_log}" 2>/dev/null || true
    else
      warn "Local ISO build failed after starting earlier in bootstrap."
      if [ -f "${early_local_iso_log}" ]; then
        echo "Local ISO build log tail (last 60 lines):"
        tail -n 60 "${early_local_iso_log}" || true
      else
        echo "Local ISO build log was not found at ${early_local_iso_log} (it may have been cleaned during the run)."
      fi
    fi
  fi
  sub_step "4.2" "Installer handoff"
  show_installer_handoff
  if [ -n "${selected_step}" ]; then
    ok "Step 4 complete."
    exit 0
  fi
fi

if [ "${first_run}" = "1" ]; then
  if [ "${dry_run}" = "1" ]; then
    adaetum_ui_completion "Dry run complete" "All five setup sections completed without changing local or provider state."
  else
    adaetum_ui_completion "Adaetum setup complete" "The private recovery repository and first-cluster installer are ready."
  fi
  adaetum_ui_key_value "Public domain" "${zone_input}"
  adaetum_ui_key_value "Tailscale tailnet" "${ts_domain}"
  adaetum_ui_key_value "Bootstrap URL" "${ks_base_url}"
else
  ok "Setup complete."
fi
