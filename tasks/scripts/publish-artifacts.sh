#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "Missing required command: ${cmd}" >&2
    exit 1
  fi
}

prepare_gh_context() {
  if [ -z "${GH_TOKEN:-}" ]; then
    if [ -n "${GITHUB_SYNC_TOKEN:-}" ]; then
      GH_TOKEN="${GITHUB_SYNC_TOKEN}"
    elif [ -n "${GITHUB_TOKEN:-}" ]; then
      GH_TOKEN="${GITHUB_TOKEN}"
    elif [ -n "${MONOREPO_GITHUB_TOKEN:-}" ]; then
      GH_TOKEN="${MONOREPO_GITHUB_TOKEN}"
    elif [ -n "${ARGOCD_GITHUB_TOKEN:-}" ]; then
      GH_TOKEN="${ARGOCD_GITHUB_TOKEN}"
    fi
  fi
  if [ -z "${GH_TOKEN:-}" ] && command -v gh >/dev/null 2>&1; then
    GH_TOKEN="$(gh auth token 2>/dev/null || true)"
  fi
  if [ -z "${GH_TOKEN:-}" ]; then
    echo "Missing GitHub token. Set one of: GH_TOKEN, GITHUB_SYNC_TOKEN, GITHUB_TOKEN, MONOREPO_GITHUB_TOKEN, ARGOCD_GITHUB_TOKEN; or login with 'gh auth login'." >&2
    exit 1
  fi
  export GH_TOKEN

  if [ -z "${GH_REPO:-}" ]; then
    remote_url="$(git config --get remote.origin.url 2>/dev/null || true)"
    if [ -n "${remote_url}" ]; then
      GH_REPO="$(printf '%s\n' "${remote_url}" | sed -E \
        -e 's#^git@github\.com:##' \
        -e 's#^https?://github\.com/##' \
        -e 's#\.git$##')"
    fi
  fi

  if [ -z "${GH_REPO:-}" ]; then
    echo "Unable to determine GitHub repo. Set GH_REPO=owner/repo." >&2
    exit 1
  fi
}

workflow_short_name() {
  printf '%s' "$1" | sed -E 's#^.*/##'
}

status_ok() {
  printf '  [ok] %s\n' "$1"
}

status_wait() {
  printf '  [wait] %s\n' "$1"
}

status_fail() {
  printf '  [fail] %s\n' "$1"
}

triggered_workflow_run_ids=()
triggered_workflow_files=()
workflow_errors=0

dispatch_workflow() {
  local workflow_file="$1"
  local branch="$2"
  local extra_input="${3:-}"
  local before_ids=""
  local run_id=""
  local new_id=""
  local try=0
  local workflow_encoded=""
  local dispatch_payload=""
  local run_json=""
  local status=""
  local conclusion=""
  local html_url=""
  local runs_json=""

  workflow_encoded="$(python3 - <<'PY' "${workflow_file}"
import sys, urllib.parse
print(urllib.parse.quote(sys.argv[1], safe=""))
PY
)"

  before_ids="$(
    curl -fsS --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
      -H "Accept: application/vnd.github+json" \
      -H "Authorization: Bearer ${GH_TOKEN}" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/${GH_REPO}/actions/workflows/${workflow_encoded}/runs?branch=${branch}&event=workflow_dispatch&per_page=30" \
      | python3 -c 'import json,sys; data=json.load(sys.stdin); [print(run.get("id")) for run in data.get("workflow_runs", []) if run.get("id") is not None]' \
      2>/dev/null | tr "\n" " " || true
  )"

  dispatch_payload="$(python3 - <<'PY' "${branch}" "${extra_input}"
import json,sys
ref=sys.argv[1]
extra=sys.argv[2]
inputs={}
if extra:
    if "=" in extra:
        k,v=extra.split("=",1)
        if k:
            inputs[k]=v
    else:
        raise SystemExit("invalid extra input format")
print(json.dumps({"ref": ref, "inputs": inputs}))
PY
)"

  curl -fsS --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
    -X POST \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer ${GH_TOKEN}" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${GH_REPO}/actions/workflows/${workflow_encoded}/dispatches" \
    -d "${dispatch_payload}" >/dev/null

  while [ "${try}" -lt 30 ]; do
    runs_json="$(curl -fsS --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
      -H "Accept: application/vnd.github+json" \
      -H "Authorization: Bearer ${GH_TOKEN}" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/${GH_REPO}/actions/workflows/${workflow_encoded}/runs?branch=${branch}&event=workflow_dispatch&per_page=30" 2>/dev/null || true)"
    if [ -n "${runs_json}" ]; then
      new_id="$(printf '%s' "${runs_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); before=set(sys.argv[1].split()); rid=""; 
for run in data.get("workflow_runs", []):
    value=str(run.get("id",""))
    if value and value != "None" and value not in before:
        rid=value
        break
print(rid)' "${before_ids}" 2>/dev/null || true)"
      if [ -n "${new_id}" ] && [ "${new_id}" != "null" ]; then
        run_id="${new_id}"
        break
      fi
    fi
    try=$((try + 1))
    sleep 2
  done

  if [ -z "${run_id}" ] || [ "${run_id}" = "null" ]; then
    status_fail "$(workflow_short_name "${workflow_file}") triggered but run id was not found"
    return 1
  fi

  triggered_workflow_run_ids+=("${run_id}")
  triggered_workflow_files+=("${workflow_file}")
  status_ok "$(workflow_short_name "${workflow_file}") triggered (run ${run_id}; deferred validation)"
  return 0
}

validate_dispatched_workflows() {
  local i=0
  local run_id=""
  local workflow_file=""
  local run_json=""
  local status=""
  local conclusion=""
  local html_url=""
  local poll=0
  local status_line=""

  if [ "${#triggered_workflow_run_ids[@]}" -eq 0 ]; then
    return 0
  fi

  echo "Validating workflow runs..."
  while [ "${i}" -lt "${#triggered_workflow_run_ids[@]}" ]; do
    run_id="${triggered_workflow_run_ids[$i]}"
    workflow_file="${triggered_workflow_files[$i]}"
    status_wait "$(workflow_short_name "${workflow_file}") run ${run_id}"
    poll=0
    while [ "${poll}" -lt 180 ]; do
      run_json="$(curl -fsS \
        --connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2 \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer ${GH_TOKEN}" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "https://api.github.com/repos/${GH_REPO}/actions/runs/${run_id}" 2>/dev/null || true)"
      if [ -n "${run_json}" ]; then
        status="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("status",""))')"
        conclusion="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("conclusion",""))')"
        html_url="$(printf '%s' "${run_json}" | python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("html_url",""))')"
        if [ "${status}" = "completed" ]; then
          if [ "${conclusion}" = "success" ]; then
            status_ok "$(workflow_short_name "${workflow_file}") succeeded (${html_url})"
          else
            status_fail "$(workflow_short_name "${workflow_file}") failed (conclusion=${conclusion})"
            [ -n "${html_url}" ] && echo "Run URL: ${html_url}"
            workflow_errors=1
          fi
          break
        fi
        if [ $((poll % 6)) -eq 0 ]; then
          status_line="status=${status:-unknown}"
          [ -n "${conclusion}" ] && status_line="${status_line}, conclusion=${conclusion}"
          status_wait "$(workflow_short_name "${workflow_file}") run ${run_id} - ${status_line}"
        fi
      fi
      poll=$((poll + 1))
      sleep 5
    done
    if [ "${poll}" -ge 180 ]; then
      status_fail "$(workflow_short_name "${workflow_file}") timed out (run ${run_id})"
      workflow_errors=1
    fi
    i=$((i + 1))
  done

  [ "${workflow_errors}" -eq 0 ]
}

prepare_gh_context
require_cmd curl
require_cmd python3
require_cmd git

branch="${PUBLISH_BRANCH:-}"
if [ -z "${branch}" ]; then
  branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
fi
if [ -z "${branch}" ] || [ "${branch}" = "HEAD" ]; then
  branch="master"
fi

iso_workflow_input=""
if [ -n "${KS_TEMPLATE_FILE_NAME:-}" ]; then
  iso_workflow_input="ks_template_file_name=${KS_TEMPLATE_FILE_NAME}"
fi

echo "Publishing artifacts for ${GH_REPO} on branch ${branch}"
dispatch_workflow ".github/workflows/ks-worker.yml" "${branch}" ""
dispatch_workflow ".github/workflows/ks-publish.yml" "${branch}" ""
dispatch_workflow ".github/workflows/iso-build.yml" "${branch}" "${iso_workflow_input}"
validate_dispatched_workflows
