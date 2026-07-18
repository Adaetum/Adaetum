# cloudflared

This app runs a Cloudflare Tunnel connector in Kubernetes.

Cloudflare does not provide a supported self-hosted Kubernetes GUI for tunnel
management. Create and manage the tunnel in the Cloudflare Zero Trust dashboard,
then run `cloudflared` here with the issued tunnel token.

## Secret

The provider-issued tunnel token is stored at
`secret/apps/cloudflared/tunnel:token` in OpenBao. External Secrets maintains
the `cloudflared/cloudflared-tunnel` delivery Secret, and Reloader restarts the
connector when that copy changes. Do not create or edit the Kubernetes Secret
directly; replace the token in Cloudflare first, then update the OpenBao field.

## Recommended setup

1. Create a remotely-managed tunnel in Cloudflare.
2. Keep Rancher on its dedicated route and use explicit hostnames for any other
   public apps you want tunneled.
3. Point selected app hostnames at the shared nginx ingress controller service
   instead of managing each service origin independently.
4. Sync this app so the connector stays online in-cluster.

Keep the tunnel management plane in Cloudflare; keep the connector runtime here.

Current setup automation supports:
- `RANCHER_PUBLIC_DOMAIN` routed to its own origin
- `CLOUDFLARED_INGRESS_PUBLIC_HOSTS` as a comma-separated allowlist of public
  hostnames routed through the nginx front door

Current setup automation defaults that allowlist to the cluster's public app
hostnames. That means Rancher and the externally exposed app routes all ride the
Cloudflare tunnel by default, while nginx ingress remains the in-cluster origin.

The shared public-app origin is expected to be:

- `INGRESS_CLOUDFLARED_ORIGIN_URL=https://rke2-ingress-nginx-controller.kube-system.svc.cluster.local:443`
- `INGRESS_CLOUDFLARED_ORIGIN_NO_TLS_VERIFY=1`

That HTTPS origin matters for Authentik-protected routes because it keeps the
redirect/request scheme aligned as `https://...` through the public tunnel path.

Override `CLOUDFLARED_INGRESS_PUBLIC_HOSTS` only when you need a narrower
allowlist. There is still no wildcard route for all subdomains.
