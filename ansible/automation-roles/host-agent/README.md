# Host agent

## What it does

Installs, upgrades, and repairs the Adaetum host agent on a server over SSH, using
the administrator access Ansible already has. The role owns the binary, the service,
the agent's token, the firewall port, and (optionally) enrolling the host with the
cluster agent. It is the lever the cluster agent's fleet view points at: a server
with no agent, or an old one, is fixed by running this against it.

Safe to run again: the binary is replaced only when its checksum differs, the access
token is created once and never rotated, the join happens once, and a changed binary
or unit restarts the service. Run it with `--check` first to see what would change.

## Defaults

The agent listens on every interface on port 8443 over HTTPS with its own
certificate, requires its access token for the API, and keeps its data in
`/var/lib/adaetum`. The firewall is left alone unless `host_agent_firewall_zone` is
set. The UI cannot remove images unless `host_agent_allow_changes` is true (the
`image-cleanup` role's timer handles routine cleanup without it).

`host_agent_auth_enabled` is off by default. When enabled, the role also installs
the separately released auth binary, a local-only Unix socket service, and a shared
secret created once. The auth database and signing key remain on the host across
updates. Specify exactly one `host_agent_auth_binary_src` or
`host_agent_auth_binary_url`; the URL also needs `host_agent_auth_binary_checksum`
in `sha256:<hex>` form.

Exactly one binary source is required. The binary runs as root everywhere, so it is
installed only from a checked source: a file on the controller, or a URL with a
required `sha256:` checksum.

## Example overrides

```bash
# Rehearse an install on one server, then do it for real:
ansible-playbook playbooks/host-agent.yml --limit node-50449 \
  -e host_agent_binary_src=dist/agent/adaetum-agent-linux-amd64 --check
ansible-playbook playbooks/host-agent.yml --limit node-50449 \
  -e host_agent_binary_src=dist/agent/adaetum-agent-linux-amd64

# Also open the port on the LAN zone and enroll with the cluster agent. Keep the
# join token in a file, not on the command line:
ansible-playbook playbooks/host-agent.yml --limit node-50449 \
  -e host_agent_binary_src=dist/agent/adaetum-agent-linux-amd64 \
  -e host_agent_firewall_zone=internal \
  -e host_agent_cluster_url=https://cluster-agent:8444 \
  -e host_agent_cluster_fingerprint=4B:AA:... \
  -e @join-token.yml      # contains: host_agent_join_token: adj_...
```
