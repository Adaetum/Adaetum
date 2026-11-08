# The grafana MCP server reads only its own credential.
path "secret/data/apps/mcp/grafana" {
  capabilities = ["read"]
}
