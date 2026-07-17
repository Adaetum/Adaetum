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

first_run_credential_namespace() {
  local origin="" normalized=""
  origin="$(adaetum_origin_url || true)"
  normalized="$(adaetum_normalize_github_url "${origin}")"
  [ -n "${normalized}" ] || normalized="local-checkout"
  printf '%s' "${normalized}"
}

first_run_github_login=""

first_run_validate_github_session() {
  local attempt=1 error_file="" error_text=""
  while [ "${attempt}" -le 3 ]; do
    error_file="$(mktemp)"
    if first_run_github_login="$(gh api user --jq '.login' 2>"${error_file}")"; then
      rm -f "${error_file}"
      return 0
    fi
    error_text="$(tr '\n' ' ' < "${error_file}")"
    rm -f "${error_file}"
    if printf '%s' "${error_text}" | grep -Eqi 'HTTP 401|Bad credentials'; then
      return 2
    fi
    if [ "${attempt}" -lt 3 ]; then
      first_run_status info "GitHub API validation was temporarily unavailable; retrying (${attempt}/3)."
      sleep 2
    fi
    attempt=$((attempt + 1))
  done
  if printf '%s' "${error_text}" | grep -Fq "invalid character '<'"; then
    error_text="GitHub's REST API returned an unavailable-service page instead of an API response."
  fi
  first_run_heading "GitHub temporarily unavailable"
  first_run_message "${ADAETUM_UI_MUTED}" "Your stored GitHub credential was preserved and no repository or provider state was changed. Check https://www.githubstatus.com, then rerun task init after API Requests recovers."
  die "Setup paused because GitHub could not validate the existing session. ${error_text}"
}

first_run_ensure_github_login() {
  local authentication_action="reused" validation_rc=0
  [ -n "${first_run_github_login}" ] && return 0
  first_run_heading "GitHub sign-in"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum reuses this GitHub session to select the recovery repository, push configuration, sync secrets, and trigger setup workflows."
  if [ "${dry_run}" = 1 ]; then
    adaetum_ui_confirm "Continue with simulated GitHub browser sign-in?" y || exit 0
    first_run_github_login="adaetum-dry-run"
    export SETUP_GITHUB_SYNC_TOKEN="ghp_dryrunplaceholdertoken0000000000000000"
    first_run_status success "GitHub sign-in validated."
    return 0
  fi

  if ! gh auth token --hostname github.com >/dev/null 2>&1; then
    adaetum_ui_confirm "Sign in to GitHub in your browser now?" y || exit 0
    gh auth login --hostname github.com --web --git-protocol https --scopes repo,workflow
    authentication_action="signed in"
  fi

  if first_run_validate_github_session; then
    validation_rc=0
  else
    validation_rc=$?
  fi
  if [ "${validation_rc}" = 2 ]; then
    adaetum_ui_confirm "The stored GitHub credential was rejected. Refresh it in your browser now?" y || exit 0
    gh auth refresh --hostname github.com --scopes repo,workflow
    authentication_action="refreshed"
    first_run_validate_github_session || die "GitHub authentication refresh completed, but the credential still could not be validated."
  elif [ "${validation_rc}" != 0 ]; then
    die "GitHub authentication validation failed."
  fi

  export SETUP_GITHUB_SYNC_TOKEN="$(gh auth token)"
  first_run_status success "GitHub session ${authentication_action} for ${first_run_github_login}."
}

first_run_set_fork_origin() {
  local fork_url="$1" origin=""
  origin="$(adaetum_origin_url || true)"

  if [ -n "${origin}" ] && adaetum_origin_is_upstream "${origin}" && ! git remote get-url upstream >/dev/null 2>&1; then
    git remote rename origin upstream
    git remote add origin "${fork_url}"
  elif git remote get-url origin >/dev/null 2>&1; then
    git remote set-url origin "${fork_url}"
  else
    git remote add origin "${fork_url}"
  fi
}

first_run_available_fork_destination=""
first_run_repository_state=""
first_run_repository_full_name=""

first_run_lookup_repository() {
  local repository="$1" error_file="" error_text=""
  first_run_repository_state=""
  first_run_repository_full_name=""
  error_file="$(mktemp)"

  if first_run_repository_full_name="$(gh api "repos/${repository}" --jq '.full_name' 2>"${error_file}")"; then
    rm -f "${error_file}"
    first_run_repository_state="exists"
    return 0
  fi

  error_text="$(tr '\n' ' ' < "${error_file}")"
  rm -f "${error_file}"
  if printf '%s' "${error_text}" | grep -Eqi 'HTTP 404|Not Found'; then
    first_run_repository_state="available"
    first_run_repository_full_name=""
    return 0
  fi
  if printf '%s' "${error_text}" | grep -Fq "invalid character '<'"; then
    error_text="GitHub's REST API returned an unavailable-service page instead of an API response."
  fi
  first_run_heading "GitHub temporarily unavailable"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum could not safely inspect ${repository}. No repository state was changed. Check https://www.githubstatus.com, then rerun task init."
  die "Repository lookup failed. ${error_text}"
}

first_run_find_available_fork_destination() {
  local owner="$1" suffix=1 candidate=""
  first_run_available_fork_destination=""

  while [ "${suffix}" -le 50 ]; do
    if [ "${suffix}" = 1 ]; then
      candidate="${owner}/Adaetum-cluster"
    else
      candidate="${owner}/Adaetum-cluster-${suffix}"
    fi
    first_run_lookup_repository "${candidate}"
    if [ "${first_run_repository_state}" = "exists" ]; then
      suffix=$((suffix + 1))
      continue
    fi
    if [ "${first_run_repository_state}" = "available" ]; then
      first_run_available_fork_destination="${candidate}"
      return 0
    fi
  done

  die "Could not find an available fork name after checking Adaetum-cluster through Adaetum-cluster-50."
}

first_run_configure_fork() {
  local origin="$(adaetum_origin_url || true)" login="" repository="" fork_url=""
  local existing="" can_push="" parent="" owner="" name="" suggested=""
  if [ -n "${origin}" ] && ! adaetum_origin_is_upstream "${origin}"; then
    first_run_heading "Fork checkout"
    first_run_status success "Using the current fork origin: ${origin}"
    return 0
  fi
  first_run_heading "Fork required"
  [ -n "${origin}" ] && first_run_message 11 "This checkout currently points to Adaetum upstream: ${origin}" || first_run_message 11 "This checkout has no origin remote."
  first_run_message "${ADAETUM_UI_MUTED}" "Setup needs your own GitHub fork for this cluster's public configuration, secret references, and recovery workflows. An unrelated repository with the same name cannot replace the fork."
  adaetum_ui_key_value "Local checkout" "${repo_root}"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum will keep using this local folder—there is nothing else to clone. It will preserve canonical Adaetum as upstream and point origin at the fork it creates or reuses."
  adaetum_ui_confirm "Create or reuse your Adaetum fork now?" y || exit 0
  first_run_ensure_github_login
  login="${first_run_github_login}"
  suggested="${login}/Adaetum"

  while :; do
    first_run_prompt_context \
      "Choose your fork destination" \
      "Enter the GitHub owner and repository name for the real Adaetum fork. Press Enter to accept the suggestion. If that name belongs to an unrelated repository, Adaetum will explain the conflict and suggest a different fork name."
    adaetum_ui_key_value "Suggested fork" "${suggested}"
    repository="$(first_run_input "GitHub fork destination (owner/name)" "${suggested}")"
    case "${repository}" in
      */*) ;;
      *) first_run_status warning "Enter the fork destination as owner/name, for example ${login}/Adaetum."; continue ;;
    esac
    owner="${repository%%/*}"
    name="${repository#*/}"
    if [ -z "${owner}" ] || [ -z "${name}" ] || [[ "${name}" == */* ]]; then
      first_run_status warning "Invalid GitHub fork destination: ${repository}"
      continue
    fi
    fork_url="https://github.com/${repository}.git"

    if [ "${dry_run}" = 1 ]; then
      adaetum_ui_confirm "Create or reuse the simulated fork ${repository}?" y || exit 0
      first_run_status info "Dry run would preserve Adaetum upstream and set origin to ${fork_url}."
      break
    fi

    first_run_lookup_repository "${repository}"
    existing="${first_run_repository_full_name}"
    if [ "${first_run_repository_state}" = "exists" ]; then
      parent="$(gh api "repos/${repository}" --jq '.parent.full_name // empty' 2>/dev/null || true)"
      if [ "$(printf '%s' "${parent}" | tr '[:upper:]' '[:lower:]')" != "adaetum/adaetum" ]; then
        first_run_status warning "${repository} already exists but is not a fork of Adaetum/Adaetum."
        first_run_find_available_fork_destination "${owner}"
        suggested="${first_run_available_fork_destination}"
        first_run_message "${ADAETUM_UI_MUTED}" "Choose a different name for the real fork. Suggested: ${suggested}"
        continue
      fi
      can_push="$(gh api "repos/${repository}" --jq '.permissions.push // false')"
      [ "${can_push}" = true ] || die "The signed-in GitHub account cannot push to ${repository}. Choose a fork you can administer."
      adaetum_ui_confirm "Reuse the existing Adaetum fork ${repository}?" y || exit 0
      first_run_status success "Fork ownership and write access validated."
      break
    fi

    adaetum_ui_confirm "Create the Adaetum fork ${repository} now?" y || exit 0
    if [ "${owner}" = "${login}" ]; then
      gum spin --title "Creating ${repository}..." -- gh repo fork Adaetum/Adaetum --clone=false --fork-name "${name}"
    else
      gum spin --title "Creating ${repository}..." -- gh repo fork Adaetum/Adaetum --clone=false --fork-name "${name}" --org "${owner}"
    fi
    break
  done

  if [ "${dry_run}" != 1 ]; then
    local attempt=0
    until gh api "repos/${repository}" --jq '.full_name' >/dev/null 2>&1; do
      attempt=$((attempt + 1)); [ "${attempt}" -lt 15 ] || die "GitHub is still preparing the fork. Rerun task init shortly."
      first_run_message 245 "Waiting for GitHub to finish preparing the fork (${attempt}/15)..."; sleep 2
    done
    first_run_set_fork_origin "${fork_url}"
  fi
  first_run_heading "Fork checkout"
  first_run_status success "Fork ready: ${fork_url}"
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
  local zone_rows="" selected_label="" zone="" account_id="" account_name="" label="" index=""
  local credential_store="${repo_root}/tasks/scripts/setup-credential-store.sh" credential_namespace="" credential_backend="" stored_token=""
  local zone_options=() zone_names=() account_ids=() account_names=()
  first_run_heading "Cloudflare account and DNS zone"
  first_run_message "${ADAETUM_UI_MUTED}" "Cloudflare calls the organization or personal workspace that owns resources an account. R2 buckets, Workers, and Tunnels belong to that account."
  first_run_message "${ADAETUM_UI_MUTED}" "A zone is one public base domain managed in Cloudflare DNS, such as example.com. Adaetum creates bootstrap and cluster service records beneath the zone you select after authorization."
  first_run_status info "Use the Cloudflare account that owns both the target DNS zone and the R2, Worker, and Tunnel resources for this cluster."

  first_run_heading "Cloudflare account token"
  first_run_message "${ADAETUM_UI_MUTED}" "In the target Cloudflare account, open Manage Account → Account API Tokens, choose Create Token, and name it Adaetum bootstrap. Creating an account token requires Super Administrator access."
  adaetum_ui_key_value "Recommended token" "Account API token (new tokens start with cfat_) — durable and not tied to one person's membership"
  adaetum_ui_key_value "Account permissions" "Account API Tokens: Edit; Workers R2 Storage: Edit; Workers Scripts: Edit; Connectivity Directory: Admin"
  adaetum_ui_key_value "Zone permissions" "Zone: Read; DNS: Edit; Workers Routes: Edit"
  first_run_message "${ADAETUM_UI_MUTED}" "Under Account Resources, include only the account that will own this cluster. Under Zone Resources, include only the public domain/zone you want Adaetum to use. These labels match the current Cloudflare dashboard; the API refers to several Edit permissions as Write."

  first_run_heading "What Adaetum will create"
  first_run_message "${ADAETUM_UI_MUTED}" "After validation, Adaetum creates or reuses the iso R2 bucket, derives a bucket-scoped upload credential, deploys the bootstrap Worker, creates a Cloudflare Tunnel, and manages the required proxied DNS records."
  if [ "${dry_run}" != 1 ]; then
    credential_namespace="$(first_run_credential_namespace)"
    credential_backend="$(bash "${credential_store}" available 2>/dev/null || true)"
    if [ -n "${credential_backend}" ]; then
      stored_token="$(bash "${credential_store}" get "${credential_namespace}" cloudflare-api-token 2>/dev/null || true)"
    fi
    if [ -n "${stored_token}" ]; then
      first_run_status info "Found a saved Cloudflare setup token in ${credential_backend}; validating it before reuse."
      if zone_rows="$(printf '%s' "${stored_token}" | python3 ./tasks/scripts/list-cloudflare-zones.py --token-stdin --details 2>/dev/null)"; then
        first_run_cloudflare_token="${stored_token}"
        first_run_status success "Reusing the validated Cloudflare account token from ${credential_backend}."
      else
        first_run_status warning "The saved Cloudflare token is no longer valid for zone discovery and will be removed from ${credential_backend}."
        bash "${credential_store}" delete "${credential_namespace}" cloudflare-api-token
      fi
    fi
    if [ -z "${first_run_cloudflare_token:-}" ]; then
      if adaetum_ui_confirm "Open Cloudflare's token page now?" y; then
        adaetum_open_url "https://dash.cloudflare.com/profile/api-tokens" || first_run_message 11 "Open Cloudflare My Profile → API Tokens, then follow the Account API Tokens link for the target account."
      fi
      adaetum_ui_confirm "I created the token and copied it. Continue to secure entry?" y || exit 0
      first_run_cloudflare_token="$(first_run_secret "Cloudflare bootstrap token")"
      [ -n "${first_run_cloudflare_token}" ] || die "Cloudflare bootstrap token is required to list your domains."
      case "${first_run_cloudflare_token}" in
        cfat_*)
          first_run_status success "Cloudflare account API token detected."
          ;;
        *)
          first_run_status warning "This does not look like a new Cloudflare account API token (cfat_). User-owned tokens are supported for compatibility but are tied to an individual account member."
          adaetum_ui_confirm "Continue with this non-account token anyway?" n || exit 0
          ;;
      esac
      first_run_status info "Validating Cloudflare access and loading each available zone with its owning account..."
      zone_rows="$(printf '%s' "${first_run_cloudflare_token}" | python3 ./tasks/scripts/list-cloudflare-zones.py --token-stdin --details)" || die "Unable to list Cloudflare zones and accounts."
      first_run_status success "Cloudflare access validated."
      if [ -n "${credential_backend}" ] && adaetum_ui_confirm "Save this token in ${credential_backend} so a cancelled setup can resume?" y; then
        printf '%s\n' "${first_run_cloudflare_token}" | bash "${credential_store}" set "${credential_namespace}" cloudflare-api-token
        first_run_status success "Cloudflare setup token saved in ${credential_backend}; no plaintext cache was created."
      fi
    fi
  else
    adaetum_ui_confirm "Continue with simulated Cloudflare authorization?" y || exit 0
    first_run_cloudflare_token="dry-run-cloudflare-token"
    zone_rows="$(python3 ./tasks/scripts/list-cloudflare-zones.py --fixture --details)"
  fi

  while IFS=$'\t' read -r zone account_id account_name; do
    [ -n "${zone}" ] && [ -n "${account_id}" ] || continue
    [ -n "${account_name}" ] || account_name="Unnamed Cloudflare account"
    label="${zone} — account: ${account_name}"
    zone_options+=("${label}")
    zone_names+=("${zone}")
    account_ids+=("${account_id}")
    account_names+=("${account_name}")
  done <<< "${zone_rows}"
  [ "${#zone_options[@]}" -gt 0 ] || die "Cloudflare returned no active zones with an owning account."

  selected_label="$(first_run_choose "Choose the public DNS zone and owning Cloudflare account" "${zone_options[@]}")"
  for index in "${!zone_options[@]}"; do
    if [ "${zone_options[${index}]}" = "${selected_label}" ]; then
      first_run_domain="${zone_names[${index}]}"
      first_run_cloudflare_account_id="${account_ids[${index}]}"
      first_run_cloudflare_account_name="${account_names[${index}]}"
      break
    fi
  done
  [ -n "${first_run_domain:-}" ] && [ -n "${first_run_cloudflare_account_id:-}" ] || die "Cloudflare zone selection did not resolve an owning account."
  first_run_status success "Selected zone ${first_run_domain} in Cloudflare account ${first_run_cloudflare_account_name}."
  export SETUP_CLOUDFLARE_API_TOKEN="${first_run_cloudflare_token}"
  export SETUP_CLOUDFLARE_ACCOUNT_ID="${first_run_cloudflare_account_id}"
  export ADAETUM_CLOUDFLARE_AUTHORIZED=1
}

first_run_select_tailscale_domain() {
  local tailnets="" credential_store="${repo_root}/tasks/scripts/setup-credential-store.sh"
  local credential_namespace="" credential_backend="" stored_token="" tailnet_input=""
  first_run_heading "Tailscale"
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum uses Tailscale for node enrollment, MagicDNS discovery, and private service access."
  first_run_heading "Temporary setup token"
  first_run_message "${ADAETUM_UI_MUTED}" "In the target tailnet, create an API access token named Adaetum setup with an expiration of 1 day. This is a short-lived bootstrap credential, not the identity your cluster will use after setup."
  adaetum_ui_key_value "Credential" "API access token"
  adaetum_ui_key_value "Expiration" "1 day — enough for setup and a retry, without leaving a durable person-bound token"
  adaetum_ui_key_value "Devices → Core" "Read — discovers the tailnet from existing device DNS names."
  adaetum_ui_key_value "Devices → Posture Attributes" "Read — required by Tailscale for Policy File access."
  adaetum_ui_key_value "Policy File" "Write — reads and adds missing Adaetum tag ownership."
  adaetum_ui_key_value "OAuth Keys" "Write — creates the durable Adaetum OAuth client after tag preparation."
  first_run_heading "Why this token is temporary"
  first_run_message "${ADAETUM_UI_MUTED}" "A user access token follows one person's Tailscale membership and has broader bootstrap authority than running nodes need. Adaetum never writes it to platform.yaml, the local .env, or GitHub secrets. If you approve secure resume storage, the one-day token is kept only in your OS credential store until it expires or is rejected."
  first_run_message "${ADAETUM_UI_MUTED}" "Instead, after tag preparation Adaetum uses this token's OAuth Keys permission to create a narrowly scoped OAuth client for repeatable node enrollment."
  first_run_heading "About the node auth key"
  first_run_message "${ADAETUM_UI_MUTED}" "You do not need to create or paste an auth key here. Setup mints a non-reusable auth key with a 1-day expiration, saves it in the gitignored local .env for the installer build, and syncs it to the fork's Prod GitHub environment. The key becomes unusable after the first successful node enrollment."
  if [ "${dry_run}" != 1 ]; then
    credential_namespace="$(first_run_credential_namespace)"
    credential_backend="$(bash "${credential_store}" available 2>/dev/null || true)"
    if [ -n "${credential_backend}" ]; then
      stored_token="$(bash "${credential_store}" get "${credential_namespace}" tailscale-api-token 2>/dev/null || true)"
    fi
    if [ -n "${stored_token}" ]; then
      first_run_status info "Found a saved Tailscale setup token in ${credential_backend}; validating it before reuse."
      if tailnets="$(printf '%s' "${stored_token}" | python3 ./tasks/scripts/list-tailscale-tailnets.py --token-stdin 2>/dev/null)"; then
        first_run_tailscale_token="${stored_token}"
        first_run_status success "Reusing the validated Tailscale API access token from ${credential_backend}."
      else
        first_run_status warning "The saved Tailscale token is invalid and will be removed from ${credential_backend}."
        bash "${credential_store}" delete "${credential_namespace}" tailscale-api-token
      fi
    fi
    if [ -z "${first_run_tailscale_token:-}" ]; then
      if adaetum_ui_confirm "Open Tailscale's access-token page now?" y; then
        adaetum_open_url "https://login.tailscale.com/admin/settings/keys" || first_run_message 11 "Open Tailscale's API key page in your browser."
      fi
      adaetum_ui_confirm "I created the token and copied it. Continue to secure entry?" y || exit 0
      first_run_tailscale_token="$(first_run_secret "Tailscale access token")"
      [ -n "${first_run_tailscale_token}" ] || die "Tailscale access token is required to find your tailnet."
      first_run_status info "Validating Tailscale access and loading available tailnets..."
      tailnets="$(printf '%s' "${first_run_tailscale_token}" | python3 ./tasks/scripts/list-tailscale-tailnets.py --token-stdin)" || die "Unable to validate Tailscale access. Check the token and required permissions shown above."
      first_run_status success "Tailscale access validated."
      if [ -n "${credential_backend}" ] && adaetum_ui_confirm "Save this one-day token in ${credential_backend} so a cancelled setup can resume?" y; then
        printf '%s\n' "${first_run_tailscale_token}" | bash "${credential_store}" set "${credential_namespace}" tailscale-api-token
        first_run_status success "Tailscale setup token saved in ${credential_backend}; no plaintext cache was created."
      fi
    fi
  else
    adaetum_ui_confirm "Continue with simulated Tailscale authorization?" y || exit 0
    first_run_tailscale_token="tskey-api-dry-run-placeholder"
    tailnets="$(python3 ./tasks/scripts/list-tailscale-tailnets.py --fixture)"
  fi
  export SETUP_TAILSCALE_USER_API_TOKEN="${first_run_tailscale_token}"
  export ADAETUM_TAILSCALE_AUTHORIZED=1
  if [ -n "${tailnets}" ]; then
    first_run_overlay_domain="$(first_run_choose "Choose the Tailscale tailnet for this cluster" ${tailnets})"
  else
    first_run_heading "New or empty tailnet"
    first_run_message "${ADAETUM_UI_MUTED}" "Tailscale validated the token, but this tailnet has no device DNS names to discover yet. This is normal for a new or emptied tailnet."
    first_run_message "${ADAETUM_UI_MUTED}" "Adaetum can continue without adding another device. The tailnet DNS name is shown in Tailscale under DNS; it looks like tailnet-name.ts.net."
    if [ "${dry_run}" != 1 ] && adaetum_ui_confirm "Open Tailscale's DNS page now?" y; then
      adaetum_open_url "https://login.tailscale.com/admin/dns" || first_run_message 11 "Open Tailscale Admin Console → DNS."
    fi
    while :; do
      tailnet_input="$(first_run_input "Tailnet DNS name (ending in .ts.net)" "")"
      tailnet_input="$(printf '%s' "${tailnet_input}" | tr '[:upper:]' '[:lower:]' | sed -E 's#^https?://##; s#/$##; s/[[:space:]]//g')"
      if [[ "${tailnet_input}" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?\.ts\.net$ ]]; then
        first_run_overlay_domain="${tailnet_input}"
        break
      fi
      first_run_status warning "Enter the tailnet DNS name shown on Tailscale's DNS page, for example tailnet-a1b2.ts.net."
    done
  fi

  first_run_heading "Prepare Tailscale tag ownership"
  first_run_message "${ADAETUM_UI_MUTED}" "This is the reason Adaetum needs the temporary API access token before the OAuth client exists. It reads the current policy and adds only missing ownership for the Adaetum node tags, making those tags available when you approve the OAuth client next."
  adaetum_ui_key_value "OAuth tags prepared" "tag:rocky10, tag:server, tag:cluster"
  if [ "${dry_run}" = 1 ]; then
    first_run_status info "Dry run would validate and prepare Tailscale tag ownership with the temporary API token."
  else
    first_run_status info "Validating and preparing Tailscale tag ownership..."
    printf '%s' "${first_run_tailscale_token}" | python3 ./tasks/scripts/bootstrap-tailscale.py \
      --user-token-stdin \
      --tailnet "${first_run_overlay_domain}" \
      --cluster-tag "tag:cluster" \
      --prepare-policy-only >/dev/null || die "Unable to prepare Tailscale tag ownership. Check that the API access token can write the policy file."
    first_run_status success "Tailscale tag ownership is ready for the OAuth client."
  fi
}

first_run_capture_tailscale_oauth() {
  local credential_store="${repo_root}/tasks/scripts/setup-credential-store.sh"
  local credential_namespace="" credential_backend="" stored_id="" stored_secret="" oauth_output="" key="" value=""
  first_run_heading "Tailscale enrollment"
  first_run_message "${ADAETUM_UI_MUTED}" "This OAuth client replaces the temporary user token for future node enrollment. Unlike the user token, it is an application identity rather than one person's identity."
  first_run_heading "OAuth client Adaetum will create"
  adaetum_ui_key_value "Description" "Adaetum node enrollment"
  adaetum_ui_key_value "Scopes → Auth Keys" "Write"
  adaetum_ui_key_value "Scopes → Policy File" "Write"
  adaetum_ui_key_value "Scopes → Devices → Core" "Read"
  adaetum_ui_key_value "Scopes → Devices → Posture Attributes" "Read"
  adaetum_ui_key_value "Tags" "tag:rocky10, tag:server, tag:cluster"
  first_run_message "${ADAETUM_UI_MUTED}" "These labels mirror Tailscale's Trust credentials topics. Adaetum creates the client through Tailscale's supported keys API, captures its one-time secret from that response, validates it, and later saves the client and generated auth key in the gitignored local .env and the fork's Prod GitHub environment."
  if [ "${dry_run}" != 1 ]; then
    credential_namespace="$(first_run_credential_namespace)"
    credential_backend="$(bash "${credential_store}" available 2>/dev/null || true)"
    if [ -n "${credential_backend}" ]; then
      stored_id="$(bash "${credential_store}" get "${credential_namespace}" tailscale-oauth-client-id 2>/dev/null || true)"
      stored_secret="$(bash "${credential_store}" get "${credential_namespace}" tailscale-oauth-client-secret 2>/dev/null || true)"
    fi
    if [ -n "${stored_id}" ] && [ -n "${stored_secret}" ]; then
      first_run_status info "Found a saved Tailscale OAuth client in ${credential_backend}; validating it before reuse."
      if printf '%s\n%s\n' "${stored_id}" "${stored_secret}" | python3 ./tasks/scripts/bootstrap-tailscale.py --oauth-credentials-stdin --validate-oauth-only >/dev/null 2>&1; then
        first_run_tailscale_oauth_client_id="${stored_id}"
        first_run_tailscale_oauth_client_secret="${stored_secret}"
        first_run_status success "Reusing the validated Tailscale OAuth client from ${credential_backend}."
      else
        first_run_status warning "The saved Tailscale OAuth client is invalid and will be removed from ${credential_backend}."
        bash "${credential_store}" delete "${credential_namespace}" tailscale-oauth-client-id
        bash "${credential_store}" delete "${credential_namespace}" tailscale-oauth-client-secret
      fi
    fi
    if [ -z "${first_run_tailscale_oauth_client_id:-}" ] || [ -z "${first_run_tailscale_oauth_client_secret:-}" ]; then
      first_run_status info "Creating the scoped Tailscale OAuth client automatically..."
      oauth_output="$(printf '%s' "${first_run_tailscale_token}" | python3 ./tasks/scripts/bootstrap-tailscale.py \
        --user-token-stdin \
        --tailnet "${first_run_overlay_domain}" \
        --cluster-tag "tag:cluster" \
        --create-oauth-client)" || die "Unable to create the Tailscale OAuth client. Ensure the API access token has OAuth Keys: Write."
      while IFS='=' read -r key value; do
        case "${key}" in
          TAILSCALE_OAUTH_CLIENT_ID) first_run_tailscale_oauth_client_id="${value}" ;;
          TAILSCALE_OAUTH_CLIENT_SECRET) first_run_tailscale_oauth_client_secret="${value}" ;;
        esac
      done <<< "${oauth_output}"
      [ -n "${first_run_tailscale_oauth_client_id:-}" ] && [ -n "${first_run_tailscale_oauth_client_secret:-}" ] || die "Tailscale created the OAuth client without returning both credentials."
      first_run_status success "Tailscale OAuth client created."
      if [ -n "${credential_backend}" ] && adaetum_ui_confirm "Save this OAuth client in ${credential_backend} so a cancelled setup can resume?" y; then
        printf '%s\n' "${first_run_tailscale_oauth_client_id}" | bash "${credential_store}" set "${credential_namespace}" tailscale-oauth-client-id
        printf '%s\n' "${first_run_tailscale_oauth_client_secret}" | bash "${credential_store}" set "${credential_namespace}" tailscale-oauth-client-secret
        first_run_status success "Tailscale OAuth client saved in ${credential_backend}; no plaintext cache was created."
      fi
    fi
  else
    first_run_status info "Dry run would create the scoped Tailscale OAuth client automatically."
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
  local proposal="$1" hook_runner=""
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
  if ! git diff --quiet HEAD -- platform.yaml; then
    git add platform.yaml
    if ! git diff --quiet HEAD -- .pre-commit-config.yaml; then
      first_run_status info "The hook configuration has uncommitted development changes; validating platform.yaml explicitly before the profile-only commit."
      if command -v prek >/dev/null 2>&1; then
        hook_runner="prek"
      elif command -v pre-commit >/dev/null 2>&1; then
        hook_runner="pre-commit"
      else
        die "The hook configuration is modified and neither prek nor pre-commit is available for explicit profile validation."
      fi
      "${hook_runner}" run --config .pre-commit-config.yaml --files platform.yaml
      git commit --no-verify -m "Configure Adaetum platform profile" -- platform.yaml
    else
      git commit -m "Configure Adaetum platform profile" -- platform.yaml
    fi
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
