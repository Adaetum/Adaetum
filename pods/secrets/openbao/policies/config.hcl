# The OpenBao service account owns declarative auth-role and ACL-policy
# configuration after the one-time root-token bootstrap. Limit this powerful
# policy to the OpenBao namespace service account through Kubernetes auth.
path "sys/auth" {
  capabilities = ["read"]
}

path "sys/auth/kubernetes" {
  capabilities = ["create", "update", "read", "sudo"]
}

path "auth/kubernetes/config" {
  capabilities = ["create", "update", "read"]
}

path "auth/kubernetes/role/*" {
  capabilities = ["create", "update", "read", "delete"]
}

path "sys/policies/acl/*" {
  capabilities = ["create", "update", "read", "delete"]
}

path "auth/token/lookup-self" {
  capabilities = ["read"]
}
