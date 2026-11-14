#!/usr/bin/env bash
# Build the host agent and the account sign-in service from this checkout and install them
# on a running development node over SSH, so a change can be tried in about a minute instead
# of rebuilding the ISO and reinstalling.
#
# Usage: dev-push.sh [host] [agent|auth|pod]
#
#   host   the node's name (for example node-54273.tailb05af2.ts.net). Left out, the one
#          node on your tailnet whose name starts with "node-" is used.
#   agent  push only the host agent;  auth  push only the sign-in service. Default: both.
#   pod    instead, build the cluster agent and sign-in images from this checkout, load them
#          into the node's container runtime and roll the cluster agent pod onto them. This
#          pauses Argo CD's application controller, because it would put the released images
#          straight back; restore it with the command printed at the end (the pod then returns
#          to the pinned release). Needs Docker on this machine.
#
# Environment: ADAETUM_DEV_USER (default "ansible", the user Tailscale SSH lets in).
#
# This replaces two binaries and restarts their services, nothing else: the token, the
# accounts, the certificate and every other file on the node are left as they are. It is for
# a development node. Pods come from the private repository or a release, so the host words
# do not touch them; use "pod" for that. A production host should be updated with the
# host-agent playbook.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
user="${ADAETUM_DEV_USER:-ansible}"
host=""
want_agent=1
want_auth=1
want_pod=0
for arg in "$@"; do
  case "${arg}" in
    agent) want_auth=0 ;;
    auth) want_agent=0 ;;
    pod) want_pod=1 ;;
    -*) echo "unknown option: ${arg}" >&2; exit 2 ;;
    *) [ -z "${host}" ] || { echo "only one host can be given (got ${host} and ${arg})" >&2; exit 2; }; host="${arg}" ;;
  esac
done
if [ "${want_pod}" = 1 ]; then want_agent=0; want_auth=0; fi
if [ "${want_pod}" = 0 ] && [ "${want_agent}" = 0 ] && [ "${want_auth}" = 0 ]; then
  echo "choose agent or auth, not both words; leave both out to push both" >&2
  exit 2
fi

if [ -z "${host}" ]; then
  tailscale_cli="$(command -v tailscale || true)"
  [ -n "${tailscale_cli}" ] || tailscale_cli="/Applications/Tailscale.app/Contents/MacOS/Tailscale"
  [ -x "${tailscale_cli}" ] || { echo "no host given and the Tailscale command was not found; pass the node's name" >&2; exit 2; }
  candidates="$("${tailscale_cli}" status --json | python3 -c '
import json, sys
status = json.load(sys.stdin)
for peer in (status.get("Peer") or {}).values():
    name = (peer.get("DNSName") or "").rstrip(".")
    if name.startswith("node-") and peer.get("Online"):
        print(name)
')"
  count="$(printf '%s\n' "${candidates}" | grep -c . || true)"
  if [ "${count}" != "1" ]; then
    echo "found ${count} online nodes on the tailnet; pass the one to push to:" >&2
    printf '%s\n' "${candidates}" | sed 's/^/  /' >&2
    exit 2
  fi
  host="${candidates}"
fi

ssh_options=(-o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new)
remote() { ssh "${ssh_options[@]}" "${user}@${host}" "$@"; }

echo "[dev-push] ${user}@${host}"
case "$(remote uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported processor on the node" >&2; exit 1 ;;
esac

if [ "${want_pod}" = 1 ]; then
  command -v docker >/dev/null 2>&1 || { echo "pod needs Docker to build the images" >&2; exit 1; }
  # A new tag every time, so the rollout always sees a change.
  tag="dev-$(git -C "${root}" rev-parse --short HEAD)-$(date +%s)"
  work="${root}/dist/dev-push"
  mkdir -p "${work}"
  for component in agent auth; do
    docker build -q --platform "linux/${arch}" --build-arg "ADAETUM_VERSION=${tag}" -t "adaetum-${component}:${tag}" "${root}/adaetum/${component}" >/dev/null
  done
  echo "[dev-push] loading adaetum-agent and adaetum-auth:${tag} into the node's container runtime"
  docker save "adaetum-agent:${tag}" "adaetum-auth:${tag}" | gzip \
    | remote 'zcat | sudo /var/lib/rancher/rke2/bin/ctr --address /run/k3s/containerd/containerd.sock -n k8s.io images import - >/dev/null'
  remote "sudo bash -s" -- "${tag}" <<'PODREMOTE'
set -euo pipefail
tag="$1"
k() { /var/lib/rancher/rke2/bin/kubectl --kubeconfig /etc/rancher/rke2/rke2.yaml "$@"; }
# Argo CD would reset the pod to the pinned release within seconds.
k -n argocd scale statefulset argocd-application-controller --replicas=0
agent="docker.io/library/adaetum-agent:${tag}"
auth="docker.io/library/adaetum-auth:${tag}"
k -n adaetum set image deploy/adaetum-cluster-agent "agent=${agent}" "initialize-credentials=${agent}" "auth=${auth}"
k -n adaetum rollout status deploy/adaetum-cluster-agent --timeout=180s
PODREMOTE
  echo "[dev-push] cluster agent pod is running this checkout. Argo CD is paused; to put the pinned release back:"
  echo "  ssh ${user}@${host} 'sudo /var/lib/rancher/rke2/bin/kubectl --kubeconfig /etc/rancher/rke2/rke2.yaml -n argocd scale statefulset argocd-application-controller --replicas=1'"
  exit 0
fi

# The service is only pushed where it is installed: production media has none, and a
# unit that is not there cannot be restarted.
have_auth_unit=0
if remote 'sudo systemctl cat adaetum-auth.service >/dev/null 2>&1'; then have_auth_unit=1; fi
if [ "${want_auth}" = 1 ] && [ "${have_auth_unit}" = 0 ]; then
  if [ "${want_agent}" = 1 ]; then
    echo "[dev-push] this node has no sign-in service installed (production media); pushing the agent only"
    want_auth=0
  else
    echo "this node has no sign-in service installed; nothing to push" >&2
    exit 1
  fi
fi

work="${root}/dist/dev-push"
mkdir -p "${work}"
files=()
if [ "${want_agent}" = 1 ]; then
  bash "${root}/tasks/scripts/build-dev-agent.sh" "${arch}" "${work}/adaetum-agent"
  files+=("${work}/adaetum-agent")
fi
if [ "${want_auth}" = 1 ]; then
  bash "${root}/tasks/scripts/build-dev-auth.sh" "${arch}" "${work}/adaetum-auth"
  files+=("${work}/adaetum-auth")
fi

stage="/tmp/adaetum-dev-push.$$"
echo "[dev-push] copying ${#files[@]} file(s)"
remote "mkdir -p ${stage}"
scp -q "${ssh_options[@]}" "${files[@]}" "${user}@${host}:${stage}/"

# Stop first so the files are not in use, install them, bring the sign-in service up before
# the agent (which connects to it), and wait for both to report active.
remote "sudo bash -s" -- "${stage}" "${want_agent}" "${want_auth}" <<'REMOTE'
set -euo pipefail
stage="$1"; agent="$2"; auth="$3"
trap 'rm -rf "${stage}"' EXIT
systemctl stop adaetum-agent.service
[ "${auth}" = 1 ] && systemctl stop adaetum-auth.service
if [ "${agent}" = 1 ]; then install -m 0755 "${stage}/adaetum-agent" /usr/local/bin/adaetum-agent; fi
if [ "${auth}" = 1 ]; then install -m 0755 "${stage}/adaetum-auth" /usr/local/bin/adaetum-auth; fi
command -v restorecon >/dev/null 2>&1 && restorecon /usr/local/bin/adaetum-agent /usr/local/bin/adaetum-auth 2>/dev/null || true
if systemctl cat adaetum-auth.service >/dev/null 2>&1; then
  systemctl start adaetum-auth.service
  sleep 2
fi
systemctl start adaetum-agent.service
failed=0
for unit in adaetum-auth.service adaetum-agent.service; do
  systemctl cat "${unit}" >/dev/null 2>&1 || continue
  state=""
  for _ in $(seq 1 15); do
    state="$(systemctl is-active "${unit}" || true)"
    [ "${state}" = "active" ] && break
    sleep 1
  done
  echo "[dev-push] ${unit}: ${state}"
  if [ "${state}" != "active" ]; then
    failed=1
    journalctl -u "${unit}" -n 12 --no-pager | cut -c1-200
  fi
done
echo "[dev-push] agent version: $(/usr/local/bin/adaetum-agent version 2>&1 | head -1)"
exit "${failed}"
REMOTE
