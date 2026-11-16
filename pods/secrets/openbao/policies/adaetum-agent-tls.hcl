# The cluster agent keeps the private key behind its TLS certificate here, so rebuilding its
# pod or losing its volume does not change the key every host pinned. It signs in as the
# adaetum-agent-tls role and may touch this one secret and nothing else.
path "secret/data/apps/adaetum/cluster-agent-tls" {
  capabilities = ["create", "read", "update"]
}
