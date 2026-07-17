#!/usr/bin/env bash

# First-run preparation belongs to the same setup process as credential capture
# and bootstrap. This file is a library, not a second wizard or entrypoint.

# shellcheck source=tasks/scripts/validate-recovery-checkout.sh
. "${repo_root}/tasks/scripts/validate-recovery-checkout.sh"

first_run_heading() {
  adaetum_ui_panel "$1"
}

first_run_message() {
  adaetum_ui_message "$1" "$2"
}

first_run_status() {
  adaetum_ui_status "$1" "$2"
}

first_run_with_progress() {
  local title="$1"
  shift
  if adaetum_gum_enabled; then
    gum spin --title "${title}" -- "$@"
    return $?
  fi
  first_run_status info "${title}"
  "$@"
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
    [ "${auto_run}" != 1 ] || die "Automatic replay requires an existing GitHub CLI login. Run task init interactively once."
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
    [ "${auto_run}" != 1 ] || die "The saved GitHub credential was rejected. Run task init interactively to refresh it."
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

first_run_ensure_github_actions() {
  local origin="" repository="" workflow_count="" attempt=1 is_fork="false"
  local default_branch="" workflow_file_count="" actions_enabled="" current_branch=""
  origin="$(adaetum_origin_url || true)"
  repository="$(adaetum_github_repository_from_url "${origin}" || true)"
  [ -n "${repository}" ] || die "Unable to determine the recovery repository for GitHub Actions setup."

  if [ "${dry_run}" = 1 ]; then
    first_run_status success "GitHub Actions registration simulated for the private recovery repository."
    return 0
  fi

  first_run_heading "GitHub workflow readiness"
  first_run_status info "Waiting for GitHub to register workflows in ${repository}..."
  while [ "${attempt}" -le 5 ]; do
    workflow_count="$(gh api "repos/${repository}/actions/workflows" --jq '.total_count // 0' 2>/dev/null || printf '0')"
    if [ "${workflow_count}" -gt 0 ] 2>/dev/null; then
      first_run_status success "GitHub Actions is enabled with ${workflow_count} registered workflow(s)."
      return 0
    fi
    sleep 2
    attempt=$((attempt + 1))
  done

  default_branch="$(gh api "repos/${repository}" --jq '.default_branch // empty' 2>/dev/null || true)"
  actions_enabled="$(gh api "repos/${repository}/actions/permissions" --jq '.enabled // false' 2>/dev/null || printf 'false')"
  workflow_file_count="$(gh api "repos/${repository}/contents/.github/workflows?ref=${default_branch}" --jq 'length' 2>/dev/null || printf '0')"

  if [ "${default_branch}" != main ] && [ "${actions_enabled}" = true ]; then
    current_branch="$(git branch --show-current)"
    first_run_heading "Initialize the workflow branch"
    first_run_message "${ADAETUM_UI_MUTED}" "Adaetum's workflow triggers use main, but this new repository currently defaults to ${default_branch}. Adaetum will publish the current commit to main and make main the repository's workflow branch. Your ${current_branch} development branch remains unchanged and available."
    adaetum_ui_confirm "Create and select the main workflow branch now?" y || exit 0
    if ! gh api "repos/${repository}/branches/main" >/dev/null 2>&1; then
      first_run_with_progress "Publishing the main workflow branch..." git push origin HEAD:refs/heads/main
    fi
    first_run_with_progress "Selecting main as the default workflow branch..." gh repo edit "${repository}" --default-branch main
    first_run_status info "Main is ready. Waiting for GitHub workflow registration..."
    attempt=1
    while [ "${attempt}" -le 10 ]; do
      workflow_count="$(gh api "repos/${repository}/actions/workflows" --jq '.total_count // 0' 2>/dev/null || printf '0')"
      if [ "${workflow_count}" -gt 0 ] 2>/dev/null; then
        first_run_status success "GitHub Actions is enabled with ${workflow_count} registered workflow(s)."
        return 0
      fi
      sleep 2
      attempt=$((attempt + 1))
    done
    default_branch="main"
    workflow_file_count="$(gh api "repos/${repository}/contents/.github/workflows?ref=main" --jq 'length' 2>/dev/null || printf '0')"
  fi

  is_fork="$(gh api "repos/${repository}" --jq '.fork // false' 2>/dev/null || printf 'false')"
  if [ "${is_fork}" = true ]; then
    first_run_heading "Enable GitHub Actions"
    first_run_message "${ADAETUM_UI_MUTED}" "This repository is still a public GitHub fork. GitHub requires one-time browser consent before its workflows run. Enable them now, then rerun setup; Adaetum will still require a private recovery repository before credentials are synchronized."
    adaetum_open_url "https://github.com/${repository}/actions" || true
    die "GitHub Actions requires one-time consent on the current public fork."
  fi
  if [ "${actions_enabled}" != true ]; then
    die "GitHub Actions is disabled for ${repository}. Enable it in Settings → Actions → General, then rerun task init."
  fi
  if [ "${workflow_file_count}" -eq 0 ] 2>/dev/null; then
    die "No workflow files are visible on ${repository}'s default branch (${default_branch})."
  fi
  die "GitHub can see ${workflow_file_count} workflow file(s) on ${default_branch}, but has not indexed them. Check the repository's Actions tab or GitHub status, then rerun task init."
}

first_run_set_recovery_origin() {
  local recovery_url="$1" origin=""
  origin="$(adaetum_origin_url || true)"

  # Keep remote names stable across retries. Renaming origin after gh has
  # inspected or created a repository can leave a partially configured checkout
  # when setup is interrupted between the rename and the replacement add.
  if ! git remote get-url upstream >/dev/null 2>&1; then
    if [ -n "${origin}" ] && adaetum_origin_is_upstream "${origin}"; then
      git remote add upstream "${origin}"
    else
      git remote add upstream "https://github.com/Adaetum/Adaetum.git"
    fi
  fi
  if git remote get-url origin >/dev/null 2>&1; then
    git remote set-url origin "${recovery_url}"
  else
    git remote add origin "${recovery_url}"
  fi
}

first_run_track_recovery_branch() {
  local current_branch=""
  current_branch="$(git branch --show-current)"
  [ -n "${current_branch}" ] || die "Setup requires a named Git branch before it can publish recovery state."

  if git ls-remote --exit-code --heads origin "${current_branch}" >/dev/null 2>&1; then
    first_run_with_progress "Refreshing the existing recovery branch..." \
      git fetch origin "+refs/heads/${current_branch}:refs/remotes/origin/${current_branch}"
    if ! git merge-base --is-ancestor "origin/${current_branch}" HEAD; then
      if ! git diff --quiet || ! git diff --cached --quiet; then
        die "The existing recovery branch must be merged, but this checkout has uncommitted changes. Commit or stash them, then rerun task init."
      fi
      first_run_status info "Restoring existing cluster configuration and recovery history from ${current_branch}..."
      if ! first_run_with_progress "Merging the existing recovery branch..." \
        git merge --no-edit "origin/${current_branch}"; then
        die "The existing recovery branch conflicts with this checkout. Resolve or abort the Git merge, then rerun task init."
      fi
    fi
    git branch --set-upstream-to="origin/${current_branch}" "${current_branch}" >/dev/null
    first_run_status success "Reusing ${current_branch} from the private recovery repository."
  else
    first_run_with_progress "Publishing the current development branch..." git push --set-upstream origin HEAD
  fi
}

first_run_move_resume_credentials() {
  local old_url="$1" new_url="$2" store="${repo_root}/tasks/scripts/setup-credential-store.sh"
  local old_namespace="" new_namespace="" key="" value="" moved=0
  [ -n "${old_url}" ] || return 0
  bash "${store}" available >/dev/null 2>&1 || return 0
  old_namespace="$(adaetum_normalize_github_url "${old_url}")"
  new_namespace="$(adaetum_normalize_github_url "${new_url}")"
  [ "${old_namespace}" != "${new_namespace}" ] || return 0

  for key in cloudflare-api-token tailscale-api-token tailscale-oauth-client-id tailscale-oauth-client-secret; do
    value="$(bash "${store}" get "${old_namespace}" "${key}" 2>/dev/null || true)"
    [ -n "${value}" ] || continue
    printf '%s\n' "${value}" | bash "${store}" set "${new_namespace}" "${key}"
    bash "${store}" delete "${old_namespace}" "${key}"
    moved=1
  done
  if [ "${moved}" = 1 ]; then
    first_run_status success "Moved protected resume credentials to the private repository namespace."
  fi
}

first_run_available_recovery_destination=""
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

first_run_find_available_recovery_destination() {
  local owner="$1" suffix=1 candidate=""
  first_run_available_recovery_destination=""

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
      first_run_available_recovery_destination="${candidate}"
      return 0
    fi
  done

  die "Could not find an available private repository name after checking Adaetum-cluster through Adaetum-cluster-50."
}

first_run_find_preferred_recovery_destination() {
  local owner="$1" candidate=""
  local visibility="" can_admin="" is_fork="" repository_size=""
  candidate="${owner}/Adaetum-cluster"

  first_run_lookup_repository "${candidate}"
  if [ "${first_run_repository_state}" = "exists" ]; then
    visibility="$(gh api "repos/${candidate}" --jq '.visibility // empty' 2>/dev/null || true)"
    can_admin="$(gh api "repos/${candidate}" --jq '.permissions.admin // false' 2>/dev/null || true)"
    is_fork="$(gh api "repos/${candidate}" --jq '.fork // false' 2>/dev/null || true)"
    repository_size="$(gh api "repos/${candidate}" --jq '.size // 0' 2>/dev/null || printf '0')"
    if [ "${visibility}" = private ] && [ "${can_admin}" = true ] && [ "${is_fork}" = false ]; then
      if [ "${repository_size}" -eq 0 ] || gh api "repos/${candidate}/contents/Taskfile.yml" >/dev/null 2>&1; then
        first_run_available_recovery_destination="${candidate}"
        return 0
      fi
    fi
  fi

  # A same-named unrelated repository is a collision; only then suggest the
  # first unused suffix instead of hiding a valid existing recovery repository.
  first_run_find_available_recovery_destination "${owner}"
}

first_run_configure_recovery_repository() {
  local origin="$(adaetum_origin_url || true)" login="" repository="" recovery_url=""
  local visibility="" can_admin="" is_fork="" owner="" name="" suggested="" current_repository="" repository_size="" created=0

  first_run_ensure_github_login
  login="${first_run_github_login}"
  if [ -n "${origin}" ] && ! adaetum_origin_is_upstream "${origin}"; then
    current_repository="$(adaetum_github_repository_from_url "${origin}" || true)"
    if [ "${dry_run}" != 1 ]; then
      visibility="$(gh api "repos/${current_repository}" --jq '.visibility // empty' 2>/dev/null || true)"
      can_admin="$(gh api "repos/${current_repository}" --jq '.permissions.admin // false' 2>/dev/null || true)"
      is_fork="$(gh api "repos/${current_repository}" --jq '.fork // false' 2>/dev/null || true)"
      if [ "${visibility}" = private ] && [ "${can_admin}" = true ] && [ "${is_fork}" = false ]; then
        repository_size="$(gh api "repos/${current_repository}" --jq '.size // 0' 2>/dev/null || printf '0')"
        if [ "${repository_size}" -eq 0 ] || gh api "repos/${current_repository}/contents/Taskfile.yml" >/dev/null 2>&1; then
          git remote get-url upstream >/dev/null 2>&1 || git remote add upstream "https://github.com/Adaetum/Adaetum.git"
          first_run_heading "Private recovery repository"
          first_run_status success "Using private origin: ${origin}"
          first_run_track_recovery_branch
          return 0
        fi
      fi
    fi
    first_run_heading "Private recovery repository required"
    first_run_message "${ADAETUM_UI_MUTED}" "The current origin is public. GitHub requires every fork of a public repository to remain public, so Adaetum uses a standalone private recovery repository instead. The current repository will not be deleted."
    first_run_message "${ADAETUM_UI_MUTED}" "If an earlier setup synchronized GitHub environment secrets to that public repository, remove them and rotate the provider credentials after this private migration completes. GitHub secrets cannot be copied back out of the old repository."
  else
    first_run_heading "Private recovery repository required"
    first_run_message "${ADAETUM_UI_MUTED}" "Adaetum stores public cluster configuration and recovery workflows in a private repository you control. Canonical Adaetum remains the read-only upstream remote."
  fi
  adaetum_ui_key_value "Local checkout" "${repo_root}"
  adaetum_ui_confirm "Create or reuse a private recovery repository now?" y || exit 0
  if [ "${dry_run}" = 1 ]; then
    suggested="${login}/Adaetum-cluster"
  else
    first_run_find_preferred_recovery_destination "${login}"
    suggested="${first_run_available_recovery_destination}"
    if [ "${auto_run}" = 1 ]; then
      first_run_lookup_repository "${suggested}"
      [ "${first_run_repository_state}" = exists ] || die "Automatic replay requires an existing private recovery repository. Run task init interactively once."
    fi
  fi

  while :; do
    first_run_prompt_context "Choose the private repository" "Enter owner/name. Adaetum creates it as private and keeps using this local checkout."
    adaetum_ui_key_value "Suggested repository" "${suggested}"
    repository="$(first_run_input "Private recovery repository (owner/name)" "${suggested}")"
    case "${repository}" in */*) ;; *) first_run_status warning "Enter owner/name, for example ${suggested}."; continue ;; esac
    owner="${repository%%/*}"; name="${repository#*/}"
    [ -n "${owner}" ] && [ -n "${name}" ] && [[ "${name}" != */* ]] || { first_run_status warning "Invalid repository name: ${repository}"; continue; }
    recovery_url="https://github.com/${repository}.git"

    if [ "${dry_run}" = 1 ]; then
      adaetum_ui_confirm "Create or reuse the simulated private repository ${repository}?" y || exit 0
      first_run_status info "Dry run would create a private repository, preserve Adaetum as upstream, and set origin to ${recovery_url}."
      break
    fi
    first_run_lookup_repository "${repository}"
    if [ "${first_run_repository_state}" = exists ]; then
      visibility="$(gh api "repos/${repository}" --jq '.visibility // empty')"
      can_admin="$(gh api "repos/${repository}" --jq '.permissions.admin // false')"
      is_fork="$(gh api "repos/${repository}" --jq '.fork // false')"
      if [ "${visibility}" != private ] || [ "${can_admin}" != true ] || [ "${is_fork}" != false ]; then
        first_run_status warning "${repository} is not a standalone private repository you can administer."
        first_run_find_available_recovery_destination "${owner}"; suggested="${first_run_available_recovery_destination}"; continue
      fi
      repository_size="$(gh api "repos/${repository}" --jq '.size // 0')"
      if [ "${repository_size}" -gt 0 ] && ! gh api "repos/${repository}/contents/Taskfile.yml" >/dev/null 2>&1; then
        first_run_status warning "${repository} is populated but does not look like Adaetum."
        first_run_find_available_recovery_destination "${owner}"; suggested="${first_run_available_recovery_destination}"; continue
      fi
      adaetum_ui_confirm "Reuse the private Adaetum recovery repository ${repository}?" y || exit 0
    else
      adaetum_ui_confirm "Create ${repository} as a private repository now?" y || exit 0
      first_run_with_progress "Creating private recovery repository..." gh repo create "${repository}" --private --description "Private Adaetum cluster configuration and recovery"
      created=1
    fi
    first_run_move_resume_credentials "${origin}" "${recovery_url}"
    first_run_set_recovery_origin "${recovery_url}"
    if [ "${created}" = 1 ]; then
      first_run_with_progress "Publishing the main workflow branch..." git push origin HEAD:refs/heads/main
      first_run_with_progress "Selecting main as the default workflow branch..." gh repo edit "${repository}" --default-branch main
    fi
    first_run_track_recovery_branch
    break
  done
  first_run_heading "Private recovery repository"
  first_run_status success "Private recovery repository ready: ${recovery_url}"
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
  # A real saved domain or tailnet is the normal rerun path. Do not leak the
  # final placeholder comparison's false status into the fail-fast wizard.
  return 0
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
  adaetum_ui_key_value "Account API tokens" "Read and Write"
  adaetum_ui_key_value "R2 storage" "Workers R2 Storage: Read and Write"
  adaetum_ui_key_value "Tunnel" "Cloudflare Tunnel: Write"
  adaetum_ui_key_value "Connectivity Directory" "Read, Bind, and Admin"
  adaetum_ui_key_value "Worker deployment" "Workers Scripts: Read and Write"
  adaetum_ui_key_value "Zone permissions" "Zone: Read; DNS: Read and Write; Workers Routes: Read and Write"
  first_run_message "${ADAETUM_UI_MUTED}" "Under Account Resources, select the entire account that will own this cluster. Under Zone Resources, select only the public domain/zone Adaetum will use. This is the current known-working Account API Token permission set; it will be narrowed only after each permission can be removed in provider regression testing."

  first_run_heading "What Adaetum will create"
  first_run_message "${ADAETUM_UI_MUTED}" "After validation, Adaetum creates or reuses the iso R2 bucket, derives a bucket-scoped upload credential, deploys the bootstrap Worker, creates a Cloudflare Tunnel, and manages the required proxied DNS records."
  if [ "${dry_run}" != 1 ]; then
    credential_namespace="$(first_run_credential_namespace)"
    credential_backend="$(bash "${credential_store}" available 2>/dev/null || true)"
    if [ "${clean_run}" != 1 ] && [ -n "${credential_backend}" ]; then
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
      [ "${auto_run}" != 1 ] || die "Automatic replay could not load a valid saved Cloudflare token. Run task init interactively to replace it."
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
      if [ -n "${credential_backend}" ] && { [ "${clean_run}" = 1 ] || adaetum_ui_confirm "Save this token in ${credential_backend} so a cancelled setup can resume?" y; }; then
        printf '%s\n' "${first_run_cloudflare_token}" | bash "${credential_store}" set "${credential_namespace}" cloudflare-api-token
        first_run_status success "Cloudflare setup token saved in ${credential_backend}; any previous value was replaced and no plaintext cache was created."
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

  selected_label=""
  if [ "${auto_run}" = 1 ] && [ "${dry_run}" != 1 ] && [ -n "${first_run_domain:-}" ]; then
    for index in "${!zone_options[@]}"; do
      if [ "${zone_names[${index}]}" = "${first_run_domain}" ]; then
        selected_label="${zone_options[${index}]}"
        first_run_status info "Automatic replay selected saved Cloudflare zone ${first_run_domain}."
        break
      fi
    done
    [ -n "${selected_label}" ] || die "Saved Cloudflare zone ${first_run_domain} is not available to the saved token. Run task init interactively to select another zone."
  else
    selected_label="$(first_run_choose "Choose the public DNS zone and owning Cloudflare account" "${zone_options[@]}")"
  fi
  for index in "${!zone_options[@]}"; do
    if [ "${zone_options[${index}]}" = "${selected_label}" ]; then
      first_run_domain="${zone_names[${index}]}"
      first_run_cloudflare_account_id="${account_ids[${index}]}"
      first_run_cloudflare_account_name="${account_names[${index}]}"
      break
    fi
  done
  [ -n "${first_run_domain:-}" ] && [ -n "${first_run_cloudflare_account_id:-}" ] || die "Cloudflare zone selection did not resolve an owning account."
  if [ "${dry_run}" = 1 ]; then
    first_run_status info "Dry run would validate Cloudflare Tunnel access for the selected account."
  elif ! printf '%s' "${first_run_cloudflare_token}" | python3 ./tasks/scripts/bootstrap-cloudflare.py \
      --token-stdin \
      --account-id "${first_run_cloudflare_account_id}" \
      --zone-domain "${first_run_domain}" \
      --validate-access-only >/dev/null 2>&1; then
    [ "${auto_run}" != 1 ] || die "The saved Cloudflare token no longer has the required access. Run task init interactively to replace it."
    local replacement_token="" replacement_rows="" replacement_zone="" replacement_account_id="" replacement_account_name=""
    local replacement_has_zone=0
    first_run_heading "Cloudflare permission required"
    first_run_message "${ADAETUM_UI_MUTED}" "Edit the Adaetum bootstrap token—or recreate it if Cloudflare does not permit editing—and apply the complete known-working permission set shown earlier. In particular, confirm Cloudflare Tunnel → Write and Connectivity Directory → Read, Bind, and Admin apply to the entire selected account."
    if adaetum_ui_confirm "Open the selected account's token page now?" y; then
      adaetum_open_url "https://dash.cloudflare.com/${first_run_cloudflare_account_id}/api-tokens/create" || true
    fi
    adaetum_ui_confirm "I updated the existing token or created and copied a replacement. Validate now?" y || exit 0

    if printf '%s' "${first_run_cloudflare_token}" | python3 ./tasks/scripts/bootstrap-cloudflare.py \
        --token-stdin \
        --account-id "${first_run_cloudflare_account_id}" \
        --zone-domain "${first_run_domain}" \
        --validate-access-only >/dev/null 2>&1; then
      first_run_status success "The existing Cloudflare token now has tunnel access."
    else
      replacement_token="$(first_run_secret "Replacement Cloudflare bootstrap token")"
      [ -n "${replacement_token}" ] || die "A replacement Cloudflare token is required to continue."
      replacement_rows="$(printf '%s' "${replacement_token}" | python3 ./tasks/scripts/list-cloudflare-zones.py --token-stdin --details)" || die "The replacement token cannot read the selected Cloudflare zone."
      while IFS=$'\t' read -r replacement_zone replacement_account_id replacement_account_name; do
        if [ "${replacement_zone}" = "${first_run_domain}" ] && [ "${replacement_account_id}" = "${first_run_cloudflare_account_id}" ]; then
          replacement_has_zone=1
          break
        fi
      done <<< "${replacement_rows}"
      [ "${replacement_has_zone}" = 1 ] || die "The replacement token is not scoped to ${first_run_domain} in the selected Cloudflare account."
      printf '%s' "${replacement_token}" | python3 ./tasks/scripts/bootstrap-cloudflare.py \
        --token-stdin \
        --account-id "${first_run_cloudflare_account_id}" \
        --zone-domain "${first_run_domain}" \
        --validate-access-only >/dev/null || die "The replacement token still lacks part of the known-working Cloudflare account or zone permission set shown above."
      first_run_cloudflare_token="${replacement_token}"
      if [ -n "${credential_backend}" ]; then
        printf '%s\n' "${first_run_cloudflare_token}" | bash "${credential_store}" set "${credential_namespace}" cloudflare-api-token
        first_run_status success "Updated the saved Cloudflare setup token in ${credential_backend}."
      fi
    fi
  fi
  first_run_status success "Selected zone ${first_run_domain} in Cloudflare account ${first_run_cloudflare_account_name}."
  export SETUP_CLOUDFLARE_API_TOKEN="${first_run_cloudflare_token}"
  export SETUP_CLOUDFLARE_ACCOUNT_ID="${first_run_cloudflare_account_id}"
  export ADAETUM_CLOUDFLARE_AUTHORIZED=1
}

first_run_load_saved_tailscale_oauth() {
  local credential_store="$1" credential_namespace="$2" credential_backend="$3"
  local stored_id="" stored_secret=""
  [ "${dry_run}" != 1 ] || return 1
  [ "${clean_run}" != 1 ] || return 1
  [ -n "${credential_backend}" ] || return 1
  if [ -n "${first_run_tailscale_oauth_client_id:-}" ] && [ -n "${first_run_tailscale_oauth_client_secret:-}" ]; then
    return 0
  fi

  stored_id="$(bash "${credential_store}" get "${credential_namespace}" tailscale-oauth-client-id 2>/dev/null || true)"
  stored_secret="$(bash "${credential_store}" get "${credential_namespace}" tailscale-oauth-client-secret 2>/dev/null || true)"
  [ -n "${stored_id}" ] && [ -n "${stored_secret}" ] || return 1
  first_run_status info "Found a saved Tailscale OAuth client in ${credential_backend}; validating it before reuse."
  if printf '%s\n%s\n' "${stored_id}" "${stored_secret}" | python3 ./tasks/scripts/bootstrap-tailscale.py --oauth-credentials-stdin --validate-oauth-only >/dev/null 2>&1; then
    first_run_tailscale_oauth_client_id="${stored_id}"
    first_run_tailscale_oauth_client_secret="${stored_secret}"
    first_run_status success "Reusing the validated Tailscale OAuth client from ${credential_backend}."
    return 0
  fi

  first_run_status warning "The saved Tailscale OAuth client is invalid and will be removed from ${credential_backend}."
  bash "${credential_store}" delete "${credential_namespace}" tailscale-oauth-client-id
  bash "${credential_store}" delete "${credential_namespace}" tailscale-oauth-client-secret
  return 1
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
  first_run_message "${ADAETUM_UI_MUTED}" "You do not need to create or paste an auth key here. Setup mints a non-reusable auth key with a 1-day expiration, saves it in the gitignored local .env for the installer build, and syncs it to the private recovery repository's Prod GitHub environment. The key becomes unusable after the first successful node enrollment."
  if [ "${dry_run}" != 1 ]; then
    credential_namespace="$(first_run_credential_namespace)"
    credential_backend="$(bash "${credential_store}" available 2>/dev/null || true)"
    first_run_load_saved_tailscale_oauth "${credential_store}" "${credential_namespace}" "${credential_backend}" || true
    if [ "${clean_run}" != 1 ] && [ -n "${credential_backend}" ]; then
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
      if [ -n "${first_run_tailscale_oauth_client_id:-}" ] && [ -n "${first_run_overlay_domain:-}" ]; then
        tailnets="${first_run_overlay_domain}"
        first_run_status success "The durable saved OAuth client replaces the expired temporary Tailscale setup token for this rerun."
      else
        [ "${auto_run}" != 1 ] || die "Automatic replay could not load durable Tailscale OAuth credentials and a saved tailnet. Run task init interactively once."
        if adaetum_ui_confirm "Open Tailscale's access-token page now?" y; then
          adaetum_open_url "https://login.tailscale.com/admin/settings/keys" || first_run_message 11 "Open Tailscale's API key page in your browser."
        fi
        adaetum_ui_confirm "I created the token and copied it. Continue to secure entry?" y || exit 0
        first_run_tailscale_token="$(first_run_secret "Tailscale access token")"
        [ -n "${first_run_tailscale_token}" ] || die "Tailscale access token is required to find your tailnet."
        first_run_status info "Validating Tailscale access and loading available tailnets..."
        tailnets="$(printf '%s' "${first_run_tailscale_token}" | python3 ./tasks/scripts/list-tailscale-tailnets.py --token-stdin)" || die "Unable to validate Tailscale access. Check the token and required permissions shown above."
        first_run_status success "Tailscale access validated."
        if [ -n "${credential_backend}" ] && { [ "${clean_run}" = 1 ] || adaetum_ui_confirm "Save this one-day token in ${credential_backend} so a cancelled setup can resume?" y; }; then
          printf '%s\n' "${first_run_tailscale_token}" | bash "${credential_store}" set "${credential_namespace}" tailscale-api-token
          first_run_status success "Tailscale setup token saved in ${credential_backend}; any previous value was replaced and no plaintext cache was created."
        fi
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
    if [ "${auto_run}" = 1 ] && [ "${dry_run}" != 1 ] && [ -n "${first_run_overlay_domain:-}" ]; then
      printf '%s\n' ${tailnets} | grep -Fxq "${first_run_overlay_domain}" || die "Saved Tailscale tailnet ${first_run_overlay_domain} is not available to the saved credentials. Run task init interactively to select another tailnet."
      first_run_status info "Automatic replay selected saved Tailscale tailnet ${first_run_overlay_domain}."
    else
      first_run_overlay_domain="$(first_run_choose "Choose the Tailscale tailnet for this cluster" ${tailnets})"
    fi
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
  elif [ -n "${first_run_tailscale_oauth_client_id:-}" ] && [ -n "${first_run_tailscale_oauth_client_secret:-}" ]; then
    first_run_status info "Validating Tailscale tag ownership with the durable OAuth client..."
    printf '%s\n%s\n' "${first_run_tailscale_oauth_client_id}" "${first_run_tailscale_oauth_client_secret}" | python3 ./tasks/scripts/bootstrap-tailscale.py \
      --oauth-credentials-stdin \
      --tailnet "${first_run_overlay_domain}" \
      --cluster-tag "tag:cluster" \
      --prepare-policy-only >/dev/null || die "Unable to validate Tailscale tag ownership with the saved OAuth client."
    first_run_status success "Tailscale tag ownership is ready."
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
  local credential_namespace="" credential_backend="" oauth_output="" key="" value=""
  first_run_heading "Tailscale enrollment"
  first_run_message "${ADAETUM_UI_MUTED}" "This OAuth client replaces the temporary user token for future node enrollment. Unlike the user token, it is an application identity rather than one person's identity."
  first_run_heading "OAuth client Adaetum will create"
  adaetum_ui_key_value "Description" "Adaetum node enrollment"
  adaetum_ui_key_value "Scopes → Auth Keys" "Write"
  adaetum_ui_key_value "Scopes → Policy File" "Write"
  adaetum_ui_key_value "Scopes → Devices → Core" "Read"
  adaetum_ui_key_value "Scopes → Devices → Posture Attributes" "Read"
  adaetum_ui_key_value "Tags" "tag:rocky10, tag:server, tag:cluster"
  first_run_message "${ADAETUM_UI_MUTED}" "These labels mirror Tailscale's Trust credentials topics. Adaetum creates the client through Tailscale's supported keys API, captures its one-time secret from that response, validates it, and later saves the client and generated auth key in the gitignored local .env and the private recovery repository's Prod GitHub environment."
  if [ "${dry_run}" != 1 ]; then
    credential_namespace="$(first_run_credential_namespace)"
    credential_backend="$(bash "${credential_store}" available 2>/dev/null || true)"
    first_run_load_saved_tailscale_oauth "${credential_store}" "${credential_namespace}" "${credential_backend}" || true
    if [ -z "${first_run_tailscale_oauth_client_id:-}" ] || [ -z "${first_run_tailscale_oauth_client_secret:-}" ]; then
      [ "${auto_run}" != 1 ] || die "Automatic replay could not load a valid saved Tailscale OAuth client. Run task init interactively once."
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
      if [ -n "${credential_backend}" ] && { [ "${clean_run}" = 1 ] || adaetum_ui_confirm "Save this OAuth client in ${credential_backend} so a cancelled setup can resume?" y; }; then
        printf '%s\n' "${first_run_tailscale_oauth_client_id}" | bash "${credential_store}" set "${credential_namespace}" tailscale-oauth-client-id
        printf '%s\n' "${first_run_tailscale_oauth_client_secret}" | bash "${credential_store}" set "${credential_namespace}" tailscale-oauth-client-secret
        first_run_status success "Tailscale OAuth client saved in ${credential_backend}; any previous values were replaced and no plaintext cache was created."
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
  first_run_message "${ADAETUM_UI_MUTED}" "Adaetum will save these public settings in your private recovery repository. Provider credentials stay in memory for this setup and are never written to platform.yaml."
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
  local proposal="$1" hook_runner="" hook_log=""
  first_run_heading "Review public cluster configuration"
  adaetum_ui_key_value "Public domain" "${first_run_domain}"
  adaetum_ui_key_value "Tailscale tailnet" "${first_run_overlay_domain}"
  first_run_message 245 "Using Adaetum standard defaults for local DNS, node tagging, the initial Gitea repository, bootstrap delivery, and R2 storage."
  adaetum_ui_confirm "Apply this public configuration to your private recovery repository?" y || exit 0
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
    if command -v prek >/dev/null 2>&1; then
      hook_runner="prek"
    elif command -v pre-commit >/dev/null 2>&1; then
      hook_runner="pre-commit"
    else
      die "Neither prek nor pre-commit is available for profile commit validation."
    fi
    hook_log="$(mktemp)"
    if "${hook_runner}" run --config .pre-commit-config.yaml --files platform.yaml >"${hook_log}" 2>&1; then
      rm -f "${hook_log}"
      first_run_status success "Profile commit checks passed."
    else
      first_run_heading "Profile commit checks failed"
      cat "${hook_log}"
      rm -f "${hook_log}"
      die "Fix the reported profile validation failure, then rerun task init."
    fi
    # The exact staged file was validated above. Avoid invoking the repository
    # hook again, which would only repeat the same result plus skipped hooks.
    git commit --no-verify -m "Configure Adaetum platform profile" -- platform.yaml
    git push origin "$(git rev-parse --abbrev-ref HEAD)"
  fi
}

first_run_installer_media() {
  local helper="${repo_root}/tasks/scripts/manage-rocky-installer-iso.sh" report path version architecture image_type size selected preferred_arch=x86_64
  local other_arch=aarch64 release_choice="" selected_release=10.2 type_choice="" selected_type=minimal arch_choice="" selected_arch="" download_size=""
  local saved_media=""
  first_run_heading "Rocky Linux installer media"
  report="$(bash "${helper}" list 2>/dev/null || true)"
  if [ -n "${report}" ]; then
    local media_options=() media_paths=() media_label="" media_index=""
    while IFS=$'\t' read -r path version architecture image_type size; do
      media_label="$(basename "${path}") — ${version}, ${architecture}, ${image_type}, ${size}"
      media_options+=("${media_label}")
      media_paths+=("${path}")
    done <<< "${report}"
    if [ "${auto_run}" = 1 ] && [ "${dry_run}" != 1 ] && [ -f .env ]; then
      saved_media="$(awk -F= '$1 == "LOCAL_ISO_PATH" {print substr($0, index($0, "=") + 1); exit}' .env | tr -d '\r\n')"
      case "${saved_media}" in
        "") ;;
        /*) ;;
        *) saved_media="${repo_root}/${saved_media}" ;;
      esac
    fi
    if [ -n "${saved_media}" ]; then
      for media_index in "${!media_paths[@]}"; do
        if [ "${media_paths[${media_index}]}" = "${saved_media}" ]; then
          selected="${media_paths[${media_index}]}"
          first_run_status info "Automatic replay selected saved installer media: $(basename "${selected}")."
          break
        fi
      done
      [ -n "${selected}" ] || die "Saved installer media is no longer available: ${saved_media}. Run task init interactively to select or download another ISO."
    elif [ "${#media_options[@]}" -eq 1 ]; then
      selected="${media_paths[0]}"
      first_run_status info "Found supported installer media: ${media_options[0]}"
    else
      media_label="$(first_run_choose "Choose discovered Rocky Linux installer media" "${media_options[@]}")"
      for media_index in "${!media_options[@]}"; do
        if [ "${media_options[${media_index}]}" = "${media_label}" ]; then
          selected="${media_paths[${media_index}]}"
          break
        fi
      done
    fi
    [ -n "${selected}" ] || die "Rocky installer selection did not resolve a local ISO."
    if [ "${dry_run}" = 1 ]; then
      export ADAETUM_INSTALLER_MEDIA_READY=1
      first_run_status success "Dry run would reuse the discovered installer ISO without downloading another copy."
    else
      first_run_status info "Verifying the existing ISO against Rocky's published SHA-256..."
      bash "${helper}" verify "${selected}" >/dev/null || die "The discovered Rocky installer ISO failed verification: ${selected}"
      first_run_status success "Existing Rocky installer ISO verified. No download is needed."
    fi
    if [ "$(dirname "${selected}")" != "${repo_root}" ]; then
      adaetum_ui_confirm "Use this installer ISO and copy it into the Adaetum checkout?" y || exit 1
      if [ "${dry_run}" = 1 ]; then
        first_run_status info "Dry run would copy $(basename "${selected}") into the checkout."
      else
        bash "${helper}" adopt "${selected}"
      fi
    fi
    return
  fi
  case "$(uname -m)" in
    arm64|aarch64) preferred_arch=aarch64; other_arch=x86_64 ;;
  esac
  first_run_message "${ADAETUM_UI_MUTED}" "No supported Rocky 10 Minimal or DVD ISO was found locally. Choose media for the machine that will run Adaetum; it may differ from this computer."
  release_choice="$(first_run_choose "Rocky Linux release" "Rocky Linux 10.2 — latest supported (recommended)")"
  case "${release_choice}" in
    "Rocky Linux 10.2"*) selected_release=10.2 ;;
    *) die "Unsupported Rocky Linux release selection." ;;
  esac
  type_choice="$(first_run_choose "Installer image type" \
    "Minimal (offline installer) — recommended, smaller download with Adaetum's required packages" \
    "DVD (offline installer) — complete package repository, much larger download")"
  case "${type_choice}" in
    Minimal*) selected_type=minimal ;;
    DVD*) selected_type=dvd ;;
    *) die "Unsupported Rocky installer image selection." ;;
  esac
  arch_choice="$(first_run_choose "Target machine architecture" \
    "${preferred_arch} — detected on this computer (recommended)" \
    "${other_arch} — build for a different target machine")"
  selected_arch="${arch_choice%% *}"
  case "${selected_arch}" in x86_64|aarch64) ;; *) die "Unsupported target architecture selection." ;; esac
  case "${selected_arch}:${selected_type}" in
    x86_64:minimal) download_size="about 1.93 GiB" ;;
    aarch64:minimal) download_size="about 2.23 GiB" ;;
    x86_64:dvd) download_size="about 9.52 GiB" ;;
    aarch64:dvd) download_size="about 9.00 GiB" ;;
  esac
  first_run_heading "Installer download"
  adaetum_ui_key_value "Release" "Rocky Linux ${selected_release} (latest supported)"
  adaetum_ui_key_value "Image" "${selected_type}"
  adaetum_ui_key_value "Target architecture" "${selected_arch}"
  adaetum_ui_key_value "Download size" "${download_size}"
  first_run_message "${ADAETUM_UI_MUTED}" "Rocky's separate Boot ISO (online installer) is not offered because it downloads packages during installation. The selected Minimal ISO is a bootable offline installer and includes the local package repository Adaetum expects."
  adaetum_ui_confirm "Download the official installer now?" y || exit 1
  if [ "${dry_run}" = 1 ]; then
    export ADAETUM_INSTALLER_MEDIA_READY=1
    export ADAETUM_DRY_RUN_ISO_NAME="Rocky-${selected_release}-${selected_arch}-$([ "${selected_type}" = dvd ] && printf dvd1 || printf minimal).iso"
    first_run_status info "Dry run would download and verify Rocky Linux ${selected_release} ${selected_type} (${selected_arch})."
  else
    bash "${helper}" download "${selected_arch}" "${selected_type}" "${selected_release}"
  fi
}

adaetum_first_run_prepare() {
  if [ "${auto_run}" != 1 ]; then
    [ -t 0 ] && [ -t 1 ] || die "task init needs an interactive terminal. Use task init:auto only after one successful interactive setup."
  else
    first_run_heading "Automatic saved-state replay"
    first_run_message "${ADAETUM_UI_MUTED}" "Adaetum will reuse the validated recovery repository, platform profile, protected provider credentials, runtime values, and installer media saved by an earlier interactive setup. No plaintext answer file is used."
  fi
  command -v task >/dev/null 2>&1 || die "Task is required to continue."
  command -v git >/dev/null 2>&1 || die "git is required to continue."
  [ "${dry_run}" = 1 ] || command -v gh >/dev/null 2>&1 || die "GitHub CLI is required. Rerun task init so it can install gh."

  first_run_phase 1 "Repository" "Create or verify the private GitHub recovery repository used to rebuild the cluster."
  first_run_configure_recovery_repository
  first_run_ensure_github_login
  first_run_ensure_github_actions
  first_run_status success "Section 1 complete — private recovery repository ready."

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
