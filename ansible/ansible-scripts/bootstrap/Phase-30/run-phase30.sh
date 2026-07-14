#!/usr/bin/env bash
set -euo pipefail

# Phase 30 installs the RKE2 platform baseline using Phase 20's temporary
# secrets. It prepares the Ansible environment explicitly so a break-glass
# invocation behaves the same regardless of its caller's working directory.

BOOTSTRAP_SECRET_DIR="${BOOTSTRAP_SECRET_DIR:-/var/lib/bootstrap-secrets}"
ANSIBLE_INVENTORY="${ANSIBLE_INVENTORY:-ansible/node-inventory.yml}"
ANSIBLE_PLAYBOOK="${ANSIBLE_PLAYBOOK:-ansible/playbooks/platform-bootstrap.yml}"
PLATFORM_DISTRIBUTION="${PLATFORM_DISTRIBUTION:-rke2}"

if [[ ! -d "${BOOTSTRAP_SECRET_DIR}" ]]; then
  echo "Missing secrets dir: ${BOOTSTRAP_SECRET_DIR}" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
cd "${repo_root}"

# Ensure we pick up repo-local config even when invoked from elsewhere.
if [[ -z "${ANSIBLE_CONFIG:-}" && -f "${repo_root}/ansible/ansible.cfg" ]]; then
  export ANSIBLE_CONFIG="${repo_root}/ansible/ansible.cfg"
fi
# Keep role discovery stable regardless of caller working directory.
export ANSIBLE_ROLES_PATH="${repo_root}/ansible/automation-roles:${repo_root}/ansible/playbooks/roles:/etc/ansible/roles:/usr/share/ansible/roles"

ansible-playbook -i "${ANSIBLE_INVENTORY}" "${ANSIBLE_PLAYBOOK}" \
  -e "bootstrap_secret_dir=${BOOTSTRAP_SECRET_DIR}" \
  -e "platform_distribution=${PLATFORM_DISTRIBUTION}"
