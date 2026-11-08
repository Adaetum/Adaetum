# The argocd MCP server reads only its own credential.
path "secret/data/apps/mcp/argocd" {
  capabilities = ["read"]
}
