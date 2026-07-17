# External Secrets may only materialize values explicitly promoted by the
# bootstrap authority. It cannot modify OpenBao or enumerate unrelated paths.
path "secret/data/apps/*" {
  capabilities = ["read"]
}

path "auth/token/lookup-self" {
  capabilities = ["read"]
}
