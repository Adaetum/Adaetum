# The agent's auth sidecar reads only the break-glass owner's login.
path "secret/data/apps/adaetum/break-glass" {
  capabilities = ["read"]
}
