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
