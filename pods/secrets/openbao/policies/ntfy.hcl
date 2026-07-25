# ntfy receives only its generated administrator and alert-publisher credentials.
path "secret/data/apps/observability/ntfy" {
  capabilities = ["read"]
}
