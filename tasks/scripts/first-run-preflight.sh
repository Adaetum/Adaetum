#!/usr/bin/env bash

# First-run preparation belongs to the same setup process as credential capture
# and bootstrap. This file is a library, not a second wizard or entrypoint.

# shellcheck source=tasks/scripts/validate-fork-checkout.sh
. "${repo_root}/tasks/scripts/validate-fork-checkout.sh"

first_run_heading() {
  adaetum_ui_panel "$1"
}

first_run_message() {
  adaetum_ui_message "$1" "$2"
}

first_run_status() {
  adaetum_ui_status "$1" "$2"
}

first_run_phase() {
  adaetum_ui_phase "$1" 5 "$2" "$3"
}

first_run_input() {
  adaetum_ui_input "$1" "$2" 0
}

first_run_secret() {
  adaetum_ui_input "$1" "" 1
}

first_run_choose() {
  local label="$1"
  shift
  adaetum_ui_choose "${label}" "$@"
}

first_run_prompt_context() {
  local title="$1" detail="$2"
  if adaetum_gum_enabled; then
    gum style --foreground "${ADAETUM_UI_ACCENT}" --bold "${title}"
    adaetum_ui_message "${ADAETUM_UI_MUTED}" "${detail}"
  else
    printf '\n%s\n%s\n' "${title}" "${detail}"
  fi
}

first_run_github_login=""

first_run_ensure_github_login() {
  [ -n "${first_run_github_login}" ] && return 0
  first_run_heading "GitHub sign-in"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum uses this GitHub session to create your fork and as the default credential for setup actions."
  if [ "${dry_run}" = 1 ]; then
    adaetum_ui_confirm "Continue with simulated GitHub browser sign-in?" y || exit 0
    first_run_github_login="adaetum-dry-run"
    export SETUP_GITHUB_SYNC_TOKEN="ghp_dryrunplaceholdertoken0000000000000000"
    first_run_status success "GitHub sign-in validated."
    return 0
  fi
  if ! gh auth status --hostname github.com >/dev/null 2>&1; then
    adaetum_ui_confirm "Sign in to GitHub in your browser now?" y || exit 0
    gh auth login --hostname github.com --web --git-protocol https --scopes repo,workflow
  fi
  first_run_github_login="$(gh api user --jq '.login')" || die "GitHub authentication did not return an account."
  export SETUP_GITHUB_SYNC_TOKEN="$(gh auth token)"
  first_run_status success "Signed in to GitHub as ${first_run_github_login}."
}

first_run_configure_fork() {
  local origin="$(adaetum_origin_url || true)" login="" fork_url="" existing="" parent=""
  if [ -n "${origin}" ] && ! adaetum_origin_is_upstream "${origin}"; then
    first_run_heading "Fork checkout"
    first_run_status success "Using fork origin: ${origin}"
    return 0
  fi
  first_run_heading "Fork required"
  [ -n "${origin}" ] && first_run_message 11 "This checkout currently points to Adaetum upstream: ${origin}" || first_run_message 11 "This checkout has no origin remote."
  first_run_message "${ADAETUM_UI_MUTED}" "Setup pushes public configuration and secret references to a fork you control."
  adaetum_ui_confirm "A fork is required to continue. Create one now?" y || exit 0
  first_run_ensure_github_login
  login="${first_run_github_login}"
  fork_url="https://github.com/${login}/Adaetum.git"
  if [ "${dry_run}" != 1 ]; then
    existing="$(gh api "repos/${login}/Adaetum" --jq '.full_name' 2>/dev/null || true)"
    if [ -n "${existing}" ]; then
      parent="$(gh api "repos/${login}/Adaetum" --jq '.parent.full_name // empty' 2>/dev/null || true)"
      [ "${parent}" = Adaetum/Adaetum ] || die "${fork_url} exists but is not an Adaetum fork."
    else
      gum spin --title "Creating ${fork_url}..." -- gh repo fork Adaetum/Adaetum --clone=false
    fi
    local attempt=0
    until git ls-remote "${fork_url}" HEAD >/dev/null 2>&1; do
      attempt=$((attempt + 1)); [ "${attempt}" -lt 15 ] || die "GitHub is still preparing the fork. Rerun task init shortly."
      first_run_message 245 "Waiting for GitHub to finish creating the fork (${attempt}/15)..."; sleep 2
    done
    git remote set-url origin "${fork_url}"
  fi
  first_run_heading "Fork checkout"
  first_run_status success "Created or confirmed your fork: ${fork_url}"
}

first_run_load_profile() {
  local key value
  while IFS='=' read -r key value; do
    case "${key}" in
      domain) first_run_domain="${value}" ;; local_domain) first_run_local_domain="${value}" ;;
      overlay_domain) first_run_overlay_domain="${value}" ;; overlay_cluster_tag) first_run_overlay_tag="${value}" ;;
      repository_owner) first_run_repository_owner="${value}" ;; repository_name) first_run_repository_name="${value}" ;;
      r2_bucket) first_run_r2_bucket="${value}" ;;
    esac
  done < <(python3 ./tasks/scripts/configure-platform-profile.py --show)
  [[ "${first_run_domain}" == *.invalid ]] && { first_run_domain=""; first_run_local_domain=""; }
  [ "${first_run_overlay_domain}" = example-tailnet.ts.net ] && first_run_overlay_domain=""
}

first_run_select_cloudflare_domain() {
  local zones="" selected_zone=""
  first_run_heading "Cloudflare DNS"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum will use Cloudflare to create the DNS records for bootstrap delivery and cluster services."
  first_run_message "${ADAETUM_UI_MUTED}" "Create an Adaetum bootstrap API token with Zone Read, DNS Edit, Workers R2 Storage Edit, Cloudflare Tunnel Edit, and Create additional tokens."
  if [ "${dry_run}" != 1 ]; then
    if adaetum_ui_confirm "Open Cloudflare's token page now?" y; then
      adaetum_open_url "https://dash.cloudflare.com/profile/api-tokens" || first_run_message 11 "Open Cloudflare's API token page in your browser."
    fi
    adaetum_ui_confirm "I created the token and copied it. Continue to secure entry?" y || exit 0
    first_run_cloudflare_token="$(first_run_secret "Cloudflare bootstrap token")"
    [ -n "${first_run_cloudflare_token}" ] || die "Cloudflare bootstrap token is required to list your domains."
    first_run_status info "Validating Cloudflare access and loading available zones..."
    zones="$(printf '%s' "${first_run_cloudflare_token}" | python3 ./tasks/scripts/list-cloudflare-zones.py --token-stdin)" || die "Unable to list Cloudflare zones."
    first_run_status success "Cloudflare access validated."
  else
    adaetum_ui_confirm "Continue with simulated Cloudflare authorization?" y || exit 0
    first_run_cloudflare_token="dry-run-cloudflare-token"
    zones="$(python3 ./tasks/scripts/list-cloudflare-zones.py --fixture)"
  fi
  export SETUP_CLOUDFLARE_API_TOKEN="${first_run_cloudflare_token}"
  export ADAETUM_CLOUDFLARE_AUTHORIZED=1
  selected_zone="$(first_run_choose "Choose the Cloudflare zone for this cluster" ${zones})"
  first_run_domain="${selected_zone}"
}

first_run_select_tailscale_domain() {
  local tailnets=""
  first_run_heading "Tailscale"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum uses Tailscale for node enrollment and private service access."
  first_run_message "${ADAETUM_UI_MUTED}" "Create an Adaetum access token with DNS Read, auth-key creation, and policy-file access. Adaetum holds it only for this setup and never writes it to platform.yaml."
  if [ "${dry_run}" != 1 ]; then
    if adaetum_ui_confirm "Open Tailscale's access-token page now?" y; then
      adaetum_open_url "https://login.tailscale.com/admin/settings/keys" || first_run_message 11 "Open Tailscale's API key page in your browser."
    fi
    adaetum_ui_confirm "I created the token and copied it. Continue to secure entry?" y || exit 0
    first_run_tailscale_token="$(first_run_secret "Tailscale access token")"
    [ -n "${first_run_tailscale_token}" ] || die "Tailscale access token is required to find your tailnet."
    first_run_status info "Validating Tailscale access and loading available tailnets..."
    tailnets="$(printf '%s' "${first_run_tailscale_token}" | python3 ./tasks/scripts/list-tailscale-tailnets.py --token-stdin)" || die "Unable to determine the Tailscale tailnet."
    first_run_status success "Tailscale access validated."
  else
    adaetum_ui_confirm "Continue with simulated Tailscale authorization?" y || exit 0
    first_run_tailscale_token="tskey-api-dry-run-placeholder"
    tailnets="$(python3 ./tasks/scripts/list-tailscale-tailnets.py --fixture)"
  fi
  export SETUP_TAILSCALE_USER_API_TOKEN="${first_run_tailscale_token}"
  export ADAETUM_TAILSCALE_AUTHORIZED=1
  first_run_overlay_domain="$(first_run_choose "Choose the Tailscale tailnet for this cluster" ${tailnets})"
}

first_run_capture_tailscale_oauth() {
  first_run_heading "Tailscale enrollment"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum needs an OAuth client for future node enrollment. Tailscale creates its client ID and secret in the admin console; Adaetum will validate the client and create the bootstrap key during bootstrap."
  first_run_message "${ADAETUM_UI_MUTED}" "Create a client with auth-key creation and policy-file access for the Adaetum node tags."
  if [ "${dry_run}" != 1 ]; then
    if adaetum_ui_confirm "Open Tailscale's OAuth client page now?" y; then
      adaetum_open_url "https://login.tailscale.com/admin/settings/oauth" || first_run_message 11 "Open Tailscale's OAuth client page in your browser."
    fi
    adaetum_ui_confirm "I created the OAuth client and copied both values. Continue to secure entry?" y || exit 0
    first_run_tailscale_oauth_client_id="$(first_run_input "Tailscale OAuth client ID" "")"
    first_run_tailscale_oauth_client_secret="$(first_run_secret "Tailscale OAuth client secret")"
    [ -n "${first_run_tailscale_oauth_client_id}" ] && [ -n "${first_run_tailscale_oauth_client_secret}" ] || die "Both Tailscale OAuth values are required."
  else
    adaetum_ui_confirm "Continue with simulated Tailscale OAuth client?" y || exit 0
    first_run_tailscale_oauth_client_id="dry-run-client-id"
    first_run_tailscale_oauth_client_secret="dry-run-client-secret"
  fi
  export SETUP_TAILSCALE_OAUTH_CLIENT_ID="${first_run_tailscale_oauth_client_id}"
  export SETUP_TAILSCALE_OAUTH_CLIENT_SECRET="${first_run_tailscale_oauth_client_secret}"
}

first_run_profile() {
  local proposal="$1" bootstrap_url=""
  first_run_heading "Configure your cluster"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum will save these public settings in your fork. Provider credentials stay in memory for this setup and are never written to platform.yaml."
  first_run_prompt_context \
    "Public cluster domain" \
    "Selected from the authorized Cloudflare zones above. Adaetum will create bootstrap and service DNS records beneath ${first_run_domain}."
  adaetum_ui_key_value "Selected" "${first_run_domain}"
  first_run_prompt_context \
    "Tailscale tailnet domain" \
    "Selected from the authorized Tailscale account above. Adaetum uses it for node enrollment and private service DNS."
  adaetum_ui_key_value "Selected" "${first_run_overlay_domain}"
  bootstrap_url="https://bootstrap.${first_run_domain}"
  first_run_local_domain="${first_run_domain}.local"
  first_run_overlay_tag="tag:cluster"
  first_run_repository_owner="gitea-admin"
  first_run_repository_name="cluster"
  first_run_bootstrap_url="${bootstrap_url}"
  first_run_r2_bucket="iso"
  python3 ./tasks/scripts/configure-platform-profile.py --profile ./platform.yaml --output "${proposal}" \
    --domain "${first_run_domain}" --local-domain "${first_run_local_domain}" --overlay-domain "${first_run_overlay_domain}" \
    --overlay-cluster-tag "${first_run_overlay_tag}" --repository-owner "${first_run_repository_owner}" \
    --repository-name "${first_run_repository_name}" --bootstrap-base-url "${first_run_bootstrap_url}" --r2-bucket "${first_run_r2_bucket}"
}

first_run_review_profile() {
  local proposal="$1"
  first_run_heading "Review public cluster configuration"
  adaetum_ui_key_value "Public domain" "${first_run_domain}"
  adaetum_ui_key_value "Tailscale tailnet" "${first_run_overlay_domain}"
  first_run_message 245 "Using Adaetum standard defaults for local DNS, node tagging, the initial Gitea repository, bootstrap delivery, and R2 storage."
  adaetum_ui_confirm "Apply this public configuration to your fork?" y || exit 0
  if [ "${dry_run}" = 1 ]; then
    export ADAETUM_PLATFORM_PROFILE="${proposal}"
    export ADAETUM_PLATFORM_PROFILE_TEMP="${proposal}"
    first_run_status info "Dry run would write, render, commit, and push this profile."
    return
  fi
  mv "${proposal}" ./platform.yaml
  task platform:render
  if ! git diff --quiet -- platform.yaml; then
    git add platform.yaml
    git commit -m "Configure Adaetum platform profile" -- platform.yaml
    git push origin "$(git rev-parse --abbrev-ref HEAD)"
  fi
}

first_run_installer_media() {
  local helper="${repo_root}/tasks/scripts/manage-rocky-installer-iso.sh" report path version architecture size selected preferred_arch=x86_64
  first_run_heading "Rocky Linux installer media"
  report="$(${helper} list 2>/dev/null || true)"
  if [ -n "${report}" ]; then
    while IFS=$'\t' read -r path version architecture size; do printf '  %s — %s, %s, %s\n' "$(basename "${path}")" "${version}" "${architecture}" "${size}"; done <<< "${report}"
    selected="$(printf '%s\n' "${report}" | cut -f1 | head -n1)"
    if [ "$(dirname "${selected}")" != "${repo_root}" ]; then
      adaetum_ui_confirm "Use this installer ISO and copy it into the Adaetum checkout?" y || exit 1
      if [ "${dry_run}" = 1 ]; then
        export ADAETUM_INSTALLER_MEDIA_READY=1
        first_run_status info "Dry run would copy $(basename "${selected}") into the checkout."
      else
        bash "${helper}" adopt "${selected}"
      fi
    fi
    return
  fi
  case "$(uname -m)" in arm64|aarch64) preferred_arch=aarch64 ;; esac
  first_run_message "${ADAETUM_UI_MUTED}" "No supported Rocky 10 Minimal ISO was found locally. Adaetum can download and SHA-256 verify Rocky Linux 10.2 Minimal for ${preferred_arch}."
  adaetum_ui_confirm "Download the official installer now?" y || exit 1
  if [ "${dry_run}" = 1 ]; then
    export ADAETUM_INSTALLER_MEDIA_READY=1
    first_run_status info "Dry run would download and verify Rocky Linux 10.2 Minimal (${preferred_arch})."
  else
    bash "${helper}" download "${preferred_arch}"
  fi
}

adaetum_first_run_prepare() {
  [ -t 0 ] && [ -t 1 ] || die "task init needs an interactive terminal."
  command -v task >/dev/null 2>&1 || die "Task is required to continue."
  command -v git >/dev/null 2>&1 || die "git is required to continue."
  [ "${dry_run}" = 1 ] || command -v gh >/dev/null 2>&1 || die "GitHub CLI is required. Rerun task init so it can install gh."

  first_run_phase 1 "Fork" "Create or verify the GitHub recovery fork used to rebuild the cluster."
  first_run_configure_fork
  first_run_ensure_github_login
  first_run_status success "Section 1 complete — recovery fork ready."

  first_run_phase 2 "Providers" "Authorize Cloudflare and Tailscale once, then reuse those credentials throughout setup."
  first_run_load_profile
  first_run_select_cloudflare_domain
  first_run_select_tailscale_domain
  first_run_capture_tailscale_oauth
  first_run_status success "Section 2 complete — provider access ready."

  first_run_phase 3 "Profile" "Review the two public cluster values before Adaetum writes the platform profile."
  local proposal
  proposal="$(mktemp)"
  first_run_profile "${proposal}"
  first_run_review_profile "${proposal}"
  [ "${dry_run}" = 1 ] || rm -f "${proposal}"
  first_run_status success "Section 3 complete — platform profile ready."

  first_run_phase 4 "Installer" "Find or download verified Rocky Linux media and validate setup readiness."
  first_run_installer_media
  if [ "${dry_run}" = 1 ]; then
    first_run_status info "Dry run would validate the reviewed profile and installer media before bootstrap begins."
  else
    task setup:preflight || die "Setup preflight is blocked after guided preparation."
  fi
  first_run_status success "Section 4 complete — installer media ready."

  first_run_phase 5 "Bootstrap" "Validate, render, publish, and prepare the first cluster installer."
}
