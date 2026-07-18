# os-bootstrap role

Prepares the OS for RKE2 installs by installing base packages, enabling required
services, and disabling swap.

## What it does

- Installs core tools and prerequisites (curl, jq, git, chrony, iSCSI).
- Installs Longhorn prerequisites (nfs-utils, cryptsetup) and loads dm_crypt.
- Ensures Kubernetes kernel modules are loaded at boot (br_netfilter, overlay).
- Applies Kubernetes networking sysctls via `/etc/sysctl.d/99-k8s.conf` and `sysctl -w`.
- Disables firewalld to avoid pod network isolation on single-node bootstrap.
- Starts and enables required services (chronyd, iscsid).
- Ensures physical wired interfaces are managed by NetworkManager with persistent
  DHCP/autoconnect profiles (`ks-ipv4-*`).
- Disables swap and comments swap entries in `/etc/fstab` (optional).

## Defaults

Defaults live in `defaults/main.yml`:

- `os_bootstrap_packages`: packages to install.
- `os_bootstrap_services`: services to enable.
- `os_bootstrap_disable_swap`: disable swap at runtime.
- `os_bootstrap_manage_fstab`: comment swap entries in `/etc/fstab`.
- `os_bootstrap_manage_networkmanager_wired`: enforce wired NM DHCP/autoconnect.
- `os_bootstrap_network_connection_prefix`: prefix for managed wired profiles.
- `os_bootstrap_network_autoconnect_priority`: autoconnect priority for wired profiles.
- `os_bootstrap_kernel_modules`: kernel modules to load at boot and runtime.
- `os_bootstrap_sysctl`: sysctl values required for Kubernetes networking.

Vars in `vars/main.yml`:

- `os_bootstrap_swap_fstab_regex`: pattern used to comment swap entries.

## Example overrides

```yaml
os_bootstrap_packages:
  - curl
  - jq
  - git
os_bootstrap_services:
  - chronyd
os_bootstrap_disable_swap: true
os_bootstrap_manage_fstab: true
```
