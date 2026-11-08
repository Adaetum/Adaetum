# The gitea MCP server reads only its own credential.
path "secret/data/apps/mcp/gitea" {
  capabilities = ["read"]
}
