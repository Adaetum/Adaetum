#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
cache_file="${repo_root}/.setup-opinionated.cache.env"
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
setup_local_iso_path=""
iso_preflight_announced=0

banner() {
  printf '%s\n' "${c_bold}${c_blue}Cluster Setup Installer${c_reset}"
  printf '%s\n' "${c_dim}Repo: ${repo_root}${c_reset}"
  printf '\n'
}

main_step() {
  local idx="$1"
  local label="$2"
  printf '\n%s\n' "${c_bold}${c_blue}[${idx}/${TOTAL_STEPS}]${c_reset} ${label}"
}

sub_step() {
  local idx="$1"
  local label="$2"
  printf '%s\n' "  - (${idx}) ${label}"
}

ok() {
  printf '%s\n' "${c_green}${1}${c_reset}"
}

warn() {
  printf '%s\n' "${c_yellow}${1}${c_reset}"
}

die() {
  printf '%s\n' "${c_red}${1}${c_reset}" >&2
  exit 1
}

prompt_value() {
  local label="$1"
  local default="${2:-}"
  local secret="${3:-0}"
  local value=""
  if [ ! -t 0 ]; then
    if [ -n "${default}" ]; then
      printf '%s' "${default}" | tr -d '\r\n'
      return 0
    fi
    echo "Missing required input for ${label} in non-interactive mode." >&2
    return 1
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

existing_env_value() {
  local file="$1"
  local key="$2"
  if [ -f "${file}" ]; then
    awk -F= -v k="${key}" '$1==k {print substr($0, index($0, "=")+1); exit}' "${file}" | tr -d '\r\n'
  fi
}

upsert_env_value() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp=""
  if [ ! -f "${file}" ]; then
    printf '%s=%s\n' "${key}" "${value}" > "${file}"
    return 0
  fi
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
    if [ -f "${cache_file}" ] && [ -n "$(normalize_compact "$(existing_env_value "${cache_file}" "${current}")")" ]; then
      return 0
    fi
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

  if [ -z "${backup_passphrase}" ] && [ -f "${cache_file}" ]; then
    backup_passphrase="$(normalize_compact "$(existing_env_value "${cache_file}" SETUP_BOOTSTRAP_BACKUP_PASSPHRASE)")"
  fi
  if [ -z "${backup_passphrase_b64}" ] && [ -f "${cache_file}" ]; then
    backup_passphrase_b64="$(normalize_compact "$(existing_env_value "${cache_file}" SETUP_BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  fi

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
      die "BOOTSTRAP_BACKUP_PASSPHRASE is missing even though prior setup state exists. Refusing to generate a new value automatically because that would orphan older emergency kits. Restore the original passphrase into .env or ${cache_file}, or rerun with SETUP_ALLOW_BACKUP_PASSPHRASE_ROTATE=1 if you are intentionally rotating it."
    fi
    backup_passphrase="$(generate_backup_passphrase)"
    fingerprint="$(backup_passphrase_fingerprint "${backup_passphrase}" || true)"
    warn "Generated new BOOTSTRAP_BACKUP_PASSPHRASE${fingerprint:+ (fingerprint ${fingerprint})}."
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

banner

if [ -f "${cache_file}" ]; then
  # shellcheck disable=SC1090
  . "${cache_file}"
fi

default_cf_token="${SETUP_CLOUDFLARE_API_TOKEN:-}"
default_gh_token="${SETUP_GITHUB_SYNC_TOKEN:-}"
default_ts_user_token="${SETUP_TAILSCALE_USER_API_TOKEN:-}"
default_ts_domain="${SETUP_TAILSCALE_DOMAIN:-}"
default_ts_oauth_client_id="${SETUP_TAILSCALE_OAUTH_CLIENT_ID:-}"
default_ts_oauth_client_secret="${SETUP_TAILSCALE_OAUTH_CLIENT_SECRET:-}"
default_backup_passphrase="${SETUP_BOOTSTRAP_BACKUP_PASSPHRASE:-}"
default_backup_passphrase_b64="${SETUP_BOOTSTRAP_BACKUP_PASSPHRASE_B64:-}"
default_zone_input="${SETUP_ZONE_INPUT:-example.services}"
default_cf_token="$(normalize_compact "${default_cf_token}")"
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
  local status_output=""
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
    status_output="$(git status --short --untracked-files=no -- "${pathspecs[@]}" || true)"
    if [ -n "${status_output}" ]; then
      echo "${status_output}"
    fi
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
}
trap cleanup EXIT

persist_cache() {
  {
    echo "# Local cache for run-opinionated-setup.sh"
    printf 'SETUP_CLOUDFLARE_API_TOKEN=%q\n' "${cf_token}"
    printf 'SETUP_GITHUB_SYNC_TOKEN=%q\n' "${gh_token}"
    printf 'SETUP_TAILSCALE_USER_API_TOKEN=%q\n' "${ts_user_token}"
    printf 'SETUP_TAILSCALE_DOMAIN=%q\n' "${ts_domain}"
    printf 'SETUP_TAILSCALE_OAUTH_CLIENT_ID=%q\n' "${ts_oauth_client_id}"
    printf 'SETUP_TAILSCALE_OAUTH_CLIENT_SECRET=%q\n' "${ts_oauth_client_secret}"
    printf 'SETUP_BOOTSTRAP_BACKUP_PASSPHRASE=%q\n' "${backup_passphrase}"
    printf 'SETUP_BOOTSTRAP_BACKUP_PASSPHRASE_B64=%q\n' "${backup_passphrase_b64}"
    printf 'SETUP_ZONE_INPUT=%q\n' "${zone_input}"
  } > "${cache_file}"
  chmod 600 "${cache_file}" 2>/dev/null || true
}

normalize_and_compute_ks_base() {
  cf_token="$(normalize_compact "${cf_token}")"
  gh_token="$(normalize_compact "${gh_token}")"
  ts_user_token="$(normalize_compact "${ts_user_token}")"
  ts_domain="$(normalize_compact "${ts_domain}")"
  zone_input="$(normalize_compact "${zone_input}")"

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

  sub_step "${substep_capture}" "Capture Tailscale OAuth credentials"
  ts_oauth_client_id="$(prompt_value "Tailscale OAuth client ID (TAILSCALE_OAUTH_CLIENT_ID)" "${default_ts_oauth_client_id}" 0)"
  ts_oauth_client_secret="$(prompt_value "Tailscale OAuth client secret (TAILSCALE_OAUTH_CLIENT_SECRET)" "${default_ts_oauth_client_secret}" 1)"
  ts_oauth_client_id="$(normalize_compact "${ts_oauth_client_id}")"
  ts_oauth_client_secret="$(normalize_compact "${ts_oauth_client_secret}")"
  if [ -z "${ts_oauth_client_id}" ] || [ -z "${ts_oauth_client_secret}" ]; then
    die "TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET are required."
  fi

  sub_step "${substep_validate}" "Validate OAuth credentials before .env/ISO render"
  cluster_tag="$(normalize_compact "$(existing_env_value .env TAILSCALE_CLUSTER_TAG)")"
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
  persist_cache
  ok "Stored and validated Tailscale OAuth credentials."
}

  if [ "${selected_substep}" = "2.2" ]; then
  main_step "2" "Generate .env"
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
  main_step "1" "Collect setup inputs"
  sub_step "1.0" "Preflight for 3.1 golden ISO upload"
  require_local_root_iso_for_golden_upload
  sub_step "1.1" "Cloudflare bootstrap token"
  cf_token="$(prompt_value "Cloudflare bootstrap token (CLOUDFLARE_API_TOKEN)" "${default_cf_token}" 1)"
  sub_step "1.2" "GitHub setup token"
  gh_token="$(prompt_value "GitHub setup token (GITHUB_SYNC_TOKEN, repo-capable and mirror-write-capable)" "${default_gh_token}" 1)"
  sub_step "1.3" "Tailscale user API token"
  ts_user_token="$(prompt_value "Tailscale user token (TAILSCALE_USER_API_TOKEN)" "${default_ts_user_token}" 1)"
  sub_step "1.4" "Tailnet DNS name"
  ts_domain="$(prompt_value "Tailnet DNS name (TAILSCALE_DOMAIN)" "${default_ts_domain}" 0)"
  sub_step "1.5" "Zone domain / KS base URL"
  zone_input="$(prompt_value "Zone domain (example: example.services)" "${default_zone_input}" 0)"
  normalize_and_compute_ks_base
  validate_required_inputs "1"
  persist_cache
  if [ -n "${selected_step}" ]; then
    ok "Step 1 complete."
    exit 0
  fi
fi

if step_enabled "2"; then
  main_step "2" "Generate .env"
  normalize_and_compute_ks_base
  validate_required_inputs "0"
  ensure_tailscale_oauth_ready "2.1" "2.2"
  ensure_cached_backup_passphrase
  persist_cache

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
  explicit_argocd_github_username="$(normalize_compact "$(existing_env_value .env ARGOCD_GITHUB_USERNAME)")"
  explicit_seed_source_username="$(normalize_compact "$(existing_env_value .env GITEA_SEED_SOURCE_USERNAME)")"
  if github_token_looks_git_capable "${gh_token}"; then
    explicit_argocd_github_token="${gh_token}"
    explicit_seed_source_token="${gh_token}"
  fi
  cat > "${tmp_existing}" <<EOF
CLOUDFLARE_API_TOKEN=${cf_token}
TAILSCALE_USER_API_TOKEN=${ts_user_token}
TAILSCALE_DOMAIN=${ts_domain}
TAILSCALE_OAUTH_CLIENT_ID=${ts_oauth_client_id}
TAILSCALE_OAUTH_CLIENT_SECRET=${ts_oauth_client_secret}
KS_BASE_URL=${ks_base_url}
GITHUB_SYNC_TOKEN=${gh_token}
ARGOCD_GITHUB_USERNAME=${explicit_argocd_github_username}
ARGOCD_GITHUB_TOKEN=${explicit_argocd_github_token}
GITEA_SEED_SOURCE_USERNAME=${explicit_seed_source_username}
GITEA_SEED_SOURCE_TOKEN=${explicit_seed_source_token}
R2_BUCKET=iso
KS_SHARED_TOKEN=${existing_ks_shared_token}
KS_UPLOAD_TOKEN=${existing_ks_upload_token}
BOOTSTRAP_BACKUP_PASSPHRASE=${existing_backup_passphrase}
BOOTSTRAP_BACKUP_PASSPHRASE_B64=${existing_backup_passphrase_b64}
EOF

  sub_step "2.3" "Render environment values"
  WRITE_VM_ENV=0 NON_INTERACTIVE=1 OPINIONATED_GITHUB_TOKEN_MODE=1 REQUIRE_CLOUDFLARE_BOOTSTRAP=1 REQUIRE_TAILSCALE_BOOTSTRAP=1 ./tasks/scripts/generate-env-files.sh .env "${tmp_existing}"
  backup_passphrase="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE)")"
  backup_passphrase_b64="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  if [ -z "${backup_passphrase}" ] || [ -z "${backup_passphrase_b64}" ]; then
    ensure_cached_backup_passphrase
    upsert_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE "${backup_passphrase}"
    upsert_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64 "${backup_passphrase_b64}"
  fi
  persist_cache
  ok "Environment file generated."

  sub_step "2.4" "Sync pods cluster config"
  py_cmd="$(resolve_python_cmd || true)"
  if [ -z "${py_cmd}" ]; then
    die "Missing required command: python3"
  fi
  "${py_cmd}" ./tasks/scripts/render-pods-config.py --env-file .env --config-file ./pods/cluster-config/cluster-config.env --sync-from-env
  "${py_cmd}" ./tasks/scripts/render-pods-config.py --check
  "${py_cmd}" ./.validator/validate-pods-consistency.py
  ok "Pods cluster config rendered."
  "${py_cmd}" ./.validator/validate-no-example-placeholders.py
  ok "Generated config is free of example placeholders."
  ./tasks/scripts/validate-opinionated-github-token-contract.sh .env
  ok "Opinionated GitHub token contract is locally valid."
  "${py_cmd}" ./.validator/validate-ingress-contract.py
  ok "Rendered ingress contract is locally valid."
  ./tasks/scripts/validate-bootstrap-runtime-env.sh
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

wizard_rc=0
if step_enabled "3"; then
  main_step "3" "Run bootstrap automation flow"
  normalize_and_compute_ks_base
  validate_required_inputs "1"
  persist_cache
  export GITHUB_SYNC_TOKEN="${gh_token}"
  export GITHUB_ENVIRONMENT="${GITHUB_ENVIRONMENT:-Prod}"
  export INITIAL_SETUP_AUTO_YES=1
  export INITIAL_SETUP_SKIP_ENV_SETUP=1
  export INITIAL_SETUP_EMBEDDED=1
  export INITIAL_SETUP_EMBEDDED_PREFIX=3
  export INITIAL_SETUP_WORKFLOW_WAIT="${INITIAL_SETUP_WORKFLOW_WAIT:-0}"
  export INITIAL_SETUP_RUN_LOCAL_ISO_BUILD=1
  export INITIAL_SETUP_EARLY_LOCAL_ISO_BUILD=0
  if ./tasks/scripts/run-initial-setup.sh; then
    wizard_rc=0
  else
    wizard_rc=$?
  fi
  if [ "${wizard_rc}" -ne 0 ]; then
    die "Setup failed: bootstrap automation flow exited with status ${wizard_rc}."
  fi
  require_local_root_iso_for_golden_upload
  backup_passphrase="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE)")"
  backup_passphrase_b64="$(normalize_compact "$(existing_env_value .env BOOTSTRAP_BACKUP_PASSPHRASE_B64)")"
  persist_cache
  "${py_cmd}" ./.validator/validate-no-example-placeholders.py
  ok "Rendered bootstrap outputs are free of example placeholders."
  "${py_cmd}" ./.validator/validate-ingress-contract.py
  ok "Rendered ingress contract is locally valid."
  ./tasks/scripts/validate-bootstrap-runtime-env.sh
  ok "Bootstrap runtime payload is locally valid."
  assert_fresh_local_iso_output "${setup_local_iso_path}"
  if [ -n "${selected_step}" ]; then
    ok "Step 3 complete."
    exit 0
  fi
fi

if step_enabled "4"; then
  main_step "4" "Finalize"
  normalize_and_compute_ks_base
  validate_required_inputs "0"
  sub_step "4.1" "Installer summary"
  if [ -n "${early_local_iso_pid}" ]; then
    sub_step "4.2" "Finalize early local ISO build"
    if wait "${early_local_iso_pid}"; then
      ok "Local ISO build completed successfully."
      rm -f "${early_local_iso_log}" 2>/dev/null || true
    else
      warn "Local ISO build failed (started in step 3/4)."
      if [ -f "${early_local_iso_log}" ]; then
        echo "Local ISO build log tail (last 60 lines):"
        tail -n 60 "${early_local_iso_log}" || true
      else
        echo "Local ISO build log was not found at ${early_local_iso_log} (it may have been cleaned during the run)."
      fi
    fi
  fi
  if [ -n "${selected_step}" ]; then
    ok "Step 4 complete."
    exit 0
  fi
fi

ok "Setup complete."
