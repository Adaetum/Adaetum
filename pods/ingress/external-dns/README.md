# external-dns (Cloudflare)

This Deployment keeps Cloudflare DNS records up to date based on annotated
Kubernetes Services.

## How it works

`external-dns` watches Services in the cluster. If a Service has the annotation:

- `external-dns.alpha.kubernetes.io/hostname: <name1>,<name2>`

it will create/update DNS records in Cloudflare that point at that Service's
LoadBalancer IP/hostname.

This repo uses that for the nginx ingress controller Service:

- `kube-system/rke2-ingress-nginx-controller` (annotation is patched by
  [`Phase-50/run-phase50.sh`](../../../ansible/ansible-scripts/bootstrap/Phase-50/run-phase50.sh))

## Secret

The zone-scoped, provider-issued token is stored at
`secret/apps/ingress/external-dns:api_token` in OpenBao. External Secrets
maintains the `ingress/external-dns-cloudflare` delivery Secret, and Reloader
restarts external-dns when it changes. Replace or revoke the credential in
Cloudflare first, then update OpenBao; do not edit the Kubernetes delivery copy.

## Domain filter

The rendered `deployment.yaml` receives its domain filter from
[`platform.yaml`](../../../platform.yaml):

- `spec.cluster.domain` becomes `--domain-filter=<your-domain>`

Do not edit the rendered deployment for a domain change. Update the profile and
run `task platform:render` instead.
