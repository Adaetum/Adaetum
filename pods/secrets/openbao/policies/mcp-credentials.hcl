# The MCP credential minter reads the administrator login of each product it issues
# a credential for, and stores the result under its own application path. It can
# write nowhere else and read nothing else.
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
