# What the cluster agent may do in OpenBao. It signs in as the adaetum-agent-tls role (short-lived,
# bound to its service account) and may touch exactly these two secrets and nothing else.
#
# The private key behind its TLS certificate. Every host pins that key, so rebuilding the pod
# or losing its volume must not change it.
path "secret/data/apps/adaetum/cluster-agent-tls" {
  capabilities = ["create", "read", "update"]
}

# Where alerts are sent (Apprise's configuration). The Notifications page adds and removes
# destinations here; each change is recorded before it is made. It also holds the ntfy
# address that bootstrap seeds, which the page never removes.
path "secret/data/apps/observability/apprise" {
  capabilities = ["create", "read", "update"]
}

# The key to the R2 bucket that scheduled backups go to. The Backups page writes it once, when
# backups are turned on; External Secrets hands it to Longhorn. The agent cannot change it later.
path "secret/data/apps/longhorn-system/backup" {
  capabilities = ["create", "read"]
}

# Sync mode (VolSync): the repository password and a copy of the bucket's key, written once when
# the first volume is put on sync. The restic password encrypts every synced volume before it
# leaves the cluster; losing it makes those copies unreadable, so the agent never overwrites it.
path "secret/data/apps/volsync/restic" {
  capabilities = ["create", "read"]
}
