#!/bin/bash
# Gives each MCP server the credential it needs and keeps it in OpenBao.
#
# For Gitea, Argo CD and Grafana this script
#   1. reads that product's administrator login from OpenBao,
#   2. checks the credential already stored for the MCP server and keeps it if the
#      product still accepts it,
#   3. otherwise has the product issue a new one through its own API,
#   4. stores it in OpenBao at secret/apps/mcp/<name>, reads it back to prove it was
#      stored, and only then revokes the superseded ones the product still holds.
# Argo CD and Grafana servers also require callers to present a token; that is made
# here too (inbound_token) and kept beside the credential.
#
# Idempotent and safe to run every few minutes: with nothing wrong it only reads. A
# product that is not up yet fails for that product alone and is tried again next run.
# Nothing here prints a credential.
set -euo pipefail

BAO_ADDR="${BAO_ADDR:-http://openbao.openbao.svc.cluster.local:8200}"
GITEA_URL="${GITEA_URL:-http://gitea-http.gitea.svc.cluster.local:3000}"
ARGOCD_URL="${ARGOCD_URL:-http://argocd-server.argocd.svc.cluster.local}"
GRAFANA_URL="${GRAFANA_URL:-http://grafana.observability.svc.cluster.local}"
WORK="${WORK:-/work}"
BAO_TOKEN=""

log() { printf '%s\n' "$*"; }

# 48 characters of URL-safe randomness for the tokens callers present.
random_token() { head -c 36 /dev/urandom | base64 | tr '+/' '-_' | tr -d '\n='; }

# --- OpenBao --------------------------------------------------------------------

bao_login() {
  local jwt
  jwt="$(cat "${SA_TOKEN_FILE:-/var/run/secrets/kubernetes.io/serviceaccount/token}")"
  BAO_TOKEN="$(
    jq -n --arg jwt "${jwt}" '{role: "mcp-credentials", jwt: $jwt}' |
      curl --silent --show-error --fail --max-time 20 --request POST --data @- \
        "${BAO_ADDR}/v1/auth/kubernetes/login" |
      jq -er '.auth.client_token'
  )"
}

# Prints the stored fields of secret/apps/<path> as JSON, or {} when nothing is stored.
bao_read() {
  local status
  status="$(
    curl --silent --show-error --max-time 20 \
      --header "X-Vault-Token: ${BAO_TOKEN}" \
      --output "${WORK}/bao.json" --write-out '%{http_code}' \
      "${BAO_ADDR}/v1/secret/data/apps/${1}"
  )"
  case "${status}" in
    200) jq -c '.data.data' "${WORK}/bao.json" ;;
    404) printf '{}' ;;
    *)
      log "OpenBao refused to read apps/${1} (HTTP ${status})." >&2
      return 1
      ;;
  esac
}

# Stores the complete field set for secret/apps/<path>.
bao_write() {
  jq -n --argjson data "${2}" '{data: $data}' |
    curl --silent --show-error --fail --max-time 20 --output /dev/null \
      --header "X-Vault-Token: ${BAO_TOKEN}" --request POST --data @- \
      "${BAO_ADDR}/v1/secret/data/apps/${1}"
}

# Writes ${2} to apps/${1} when it differs from what is stored, and proves it stuck.
persist() {
  local path="${1}" desired="${2}" stored
  stored="$(bao_read "${path}")"
  if [[ "$(jq -S . <<<"${stored}")" == "$(jq -S . <<<"${desired}")" ]]; then
    return 0
  fi
  bao_write "${path}" "${desired}"
  stored="$(bao_read "${path}")"
  [[ "$(jq -S . <<<"${stored}")" == "$(jq -S . <<<"${desired}")" ]]
}

# --- Gitea ----------------------------------------------------------------------

mint_gitea() {
  local admin user password current token response new_token name id
  admin="$(bao_read gitea/admin)"
  user="$(jq -er '.username' <<<"${admin}")"
  password="$(jq -er '.password' <<<"${admin}")"
  current="$(bao_read mcp/gitea)"
  token="$(jq -r '.token // empty' <<<"${current}")"

  if [[ -n "${token}" ]] && curl --silent --fail --max-time 20 --output /dev/null \
      --header "Authorization: token ${token}" "${GITEA_URL}/api/v1/user"; then
    log "gitea: the stored token is valid."
    return 0
  fi

  # Full access to the administrator's account: the server is meant to do whatever
  # an administrator can in Gitea.
  name="adaetum-mcp-$(date +%s)"
  response="$(
    jq -n --arg name "${name}" '{name: $name, scopes: ["all"]}' |
      curl --silent --show-error --fail --max-time 20 \
        --user "${user}:${password}" --header 'Content-Type: application/json' \
        --request POST --data @- "${GITEA_URL}/api/v1/users/${user}/tokens"
  )"
  new_token="$(jq -er '.sha1 // .token' <<<"${response}")"
  persist mcp/gitea "$(jq -c --arg token "${new_token}" '. + {token: $token}' <<<"${current}")"

  # The new token is stored; revoke the ones it replaces.
  curl --silent --show-error --fail --max-time 20 --user "${user}:${password}" \
    "${GITEA_URL}/api/v1/users/${user}/tokens" |
    jq -r --arg keep "${name}" '.[] | select(.name | startswith("adaetum-mcp-")) | select(.name != $keep) | .id' |
    while read -r id; do
      curl --silent --show-error --fail --max-time 20 --user "${user}:${password}" \
        --request DELETE --output /dev/null "${GITEA_URL}/api/v1/users/${user}/tokens/${id}"
    done
  log "gitea: issued a new token and stored it."
}

# --- Argo CD --------------------------------------------------------------------

mint_argocd() {
  local admin password session current token inbound response id updated
  admin="$(bao_read argocd/admin)"
  password="$(jq -er '.password' <<<"${admin}")"
  current="$(bao_read mcp/argocd)"
  token="$(jq -r '.token // empty' <<<"${current}")"
  inbound="$(jq -r '.inbound_token // empty' <<<"${current}")"
  updated="${current}"

  if [[ -z "${token}" ]] || ! curl --silent --fail --max-time 20 \
      --header "Authorization: Bearer ${token}" "${ARGOCD_URL}/api/v1/session/userinfo" |
      jq -e '.loggedIn == true and .username == "mcp"' >/dev/null; then
    session="$(
      jq -n --arg password "${password}" '{username: "admin", password: $password}' |
        curl --silent --show-error --fail --max-time 20 \
          --header 'Content-Type: application/json' --request POST --data @- \
          "${ARGOCD_URL}/api/v1/session" |
        jq -er '.token'
    )"
    # The account itself is declared in Argo CD's configuration (accounts.mcp in the
    # argocd-install values). Without it there is nothing to issue a token for.
    if ! curl --silent --fail --max-time 20 --output /dev/null \
        --header "Authorization: Bearer ${session}" "${ARGOCD_URL}/api/v1/account/mcp"; then
      log "argocd: Argo CD has no 'mcp' account yet; it is declared by the argocd-install values." >&2
      return 1
    fi
    id="adaetum-mcp-$(date +%s)"
    response="$(
      jq -n --arg id "${id}" '{id: $id, expiresIn: 0}' |
        curl --silent --show-error --fail --max-time 20 \
          --header "Authorization: Bearer ${session}" --header 'Content-Type: application/json' \
          --request POST --data @- "${ARGOCD_URL}/api/v1/account/mcp/token"
    )"
    token="$(jq -er '.token' <<<"${response}")"
    updated="$(jq -c --arg token "${token}" '. + {token: $token}' <<<"${updated}")"
  else
    log "argocd: the stored token is valid."
  fi

  if [[ -z "${inbound}" ]]; then
    updated="$(jq -c --arg inbound "$(random_token)" '. + {inbound_token: $inbound}' <<<"${updated}")"
  fi
  persist mcp/argocd "${updated}"

  if [[ -n "${id:-}" ]]; then
    curl --silent --show-error --fail --max-time 20 \
      --header "Authorization: Bearer ${session}" "${ARGOCD_URL}/api/v1/account/mcp" |
      jq -r --arg keep "${id}" '.tokens[]? | select(.id != $keep) | .id' |
      while read -r old; do
        curl --silent --show-error --fail --max-time 20 --output /dev/null \
          --header "Authorization: Bearer ${session}" --request DELETE \
          "${ARGOCD_URL}/api/v1/account/mcp/token/${old}"
      done
    log "argocd: issued a new token and stored it."
  fi
}

# --- Grafana --------------------------------------------------------------------

mint_grafana() {
  local admin user password current token inbound id name response updated status
  admin="$(bao_read observability/grafana)"
  user="$(jq -er '.admin_user' <<<"${admin}")"
  password="$(jq -er '.admin_password' <<<"${admin}")"
  current="$(bao_read mcp/grafana)"
  token="$(jq -r '.token // empty' <<<"${current}")"
  inbound="$(jq -r '.inbound_token // empty' <<<"${current}")"
  updated="${current}"
  name=""

  if [[ -z "${token}" ]] || ! curl --silent --fail --max-time 20 --output /dev/null \
      --header "Authorization: Bearer ${token}" "${GRAFANA_URL}/api/org"; then
    # One service account with the Admin role: the server is meant to do whatever an
    # administrator can in Grafana. Tokens come and go; the account stays.
    status="$(
      jq -n '{name: "adaetum-mcp", role: "Admin"}' |
        curl --silent --show-error --max-time 20 --user "${user}:${password}" \
          --header 'Content-Type: application/json' --request POST --data @- \
          --output "${WORK}/serviceaccount.json" --write-out '%{http_code}' \
          "${GRAFANA_URL}/api/serviceaccounts"
    )"
    case "${status}" in
      200 | 201) id="$(jq -er '.id' "${WORK}/serviceaccount.json")" ;;
      409)
        id="$(
          curl --silent --show-error --fail --max-time 20 --user "${user}:${password}" --get \
            --data-urlencode 'query=adaetum-mcp' "${GRAFANA_URL}/api/serviceaccounts/search" |
            jq -er '.serviceAccounts[] | select(.name == "adaetum-mcp") | .id'
        )"
        ;;
      *)
        log "grafana: could not create the service account (HTTP ${status})." >&2
        return 1
        ;;
    esac
    name="adaetum-mcp-$(date +%s)"
    response="$(
      jq -n --arg name "${name}" '{name: $name}' |
        curl --silent --show-error --fail --max-time 20 --user "${user}:${password}" \
          --header 'Content-Type: application/json' --request POST --data @- \
          "${GRAFANA_URL}/api/serviceaccounts/${id}/tokens"
    )"
    token="$(jq -er '.key' <<<"${response}")"
    updated="$(jq -c --arg token "${token}" '. + {token: $token}' <<<"${updated}")"
  else
    log "grafana: the stored token is valid."
  fi

  if [[ -z "${inbound}" ]]; then
    updated="$(jq -c --arg inbound "$(random_token)" '. + {inbound_token: $inbound}' <<<"${updated}")"
  fi
  persist mcp/grafana "${updated}"

  if [[ -n "${name}" ]]; then
    curl --silent --show-error --fail --max-time 20 --user "${user}:${password}" \
      "${GRAFANA_URL}/api/serviceaccounts/${id}/tokens" |
      jq -r --arg keep "${name}" '.[] | select(.name | startswith("adaetum-mcp-")) | select(.name != $keep) | .id' |
      while read -r old; do
        curl --silent --show-error --fail --max-time 20 --user "${user}:${password}" \
          --request DELETE --output /dev/null "${GRAFANA_URL}/api/serviceaccounts/${id}/tokens/${old}"
      done
    log "grafana: issued a new token and stored it."
  fi
}

# --- Run ------------------------------------------------------------------------

bao_login
failed=0
for service in gitea argocd grafana; do
  # A subshell run as its own command keeps `set -e` in force inside it, so the first
  # failing step stops that product only; the others still get their turn.
  set +e
  ( set -e; "mint_${service}" )
  status=$?
  set -e
  if [[ "${status}" -ne 0 ]]; then
    log "${service}: not done this run; it will be tried again." >&2
    failed=1
  fi
done
exit "${failed}"
