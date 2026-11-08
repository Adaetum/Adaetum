# The MCP credential minter reads the administrator login of each product it issues
# a credential for, and stores the result under its own application path. It can
# also issues the separate agent update token, and reads or writes no other paths.
path "secret/data/apps/gitea/admin" {
  capabilities = ["read"]
}

path "secret/data/apps/argocd/admin" {
  capabilities = ["read"]
}

path "secret/data/apps/observability/grafana" {
  capabilities = ["read"]
}

path "secret/data/apps/mcp/gitea" {
  capabilities = ["create", "read", "update"]
}

path "secret/data/apps/mcp/argocd" {
  capabilities = ["create", "read", "update"]
}

path "secret/data/apps/mcp/grafana" {
  capabilities = ["create", "read", "update"]
}

# The agent update token is separate from the full-access MCP token.
path "secret/data/apps/adaetum/gitops-update" {
  capabilities = ["create", "read", "update"]
}
