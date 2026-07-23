## Homepage

This app provides a baseline cluster portal with curated internal and public
links plus built-in health indicators.

What it does
- Exposes one consistent service catalog for every known operator-facing UI.
- Uses Homepage's `siteMonitor` feature for route status.
- Uses native Homepage widgets where supported. The current setup forces the
  stable in-cluster widgets on for Argo CD, Gitea, Grafana, and Prometheus.
- Provides a low-friction landing page for users before they know which UI or
  route they need.

Notes
- Internal and public views render the same cards, groups, widgets, and labels.
  The browser changes only the destination hostname: `.local` visitors receive
  local ingress links while public visitors receive public ingress links.
- Rancher has an Adaetum-owned local ingress so its card follows the same
  environment-aware link behavior as the other cluster UIs.
- Homepage monitors from the cluster, not from each user's browser, so this is
  cluster-vantage health rather than true end-user ISP/LAN vantage.
- `config/bookmarks.yaml` is intentionally empty so Homepage does not render its
  stock Developer/Social/Entertainment bookmark groups.
- Argo CD and Gitea widget tokens are delivered from OpenBao
  `secret/apps/homepage/widgets` through Secrets Store CSI. These tokens cannot
  be arbitrary OpenBao-generated values: Argo CD signs its tokens and Gitea
  registers its access tokens. Late bootstrap therefore validates or mints a
  replacement through the owning app, persists and read-verifies it in OpenBao,
  and only then restarts Homepage. Authentik, Cloudflare, and Tailscale are links
  only, so their credentials are never delivered to Homepage. Late
  reconciliation also checks the Gitea token's registered scopes and revokes
  superseded `homepage-widget*` tokens only after the replacement is active.
- Grafana uses a separate `homepage` Viewer identity from
  `secret/apps/homepage/grafana`; Homepage never receives Grafana's
  administrator password. A scoped reconciler changes the account in Grafana,
  verifies the desired login, and then promotes the active Homepage delivery
  Secret. Reloader restarts Homepage only after that promotion succeeds.
- Route status is intentionally `siteMonitor`-only. The old `ping` checks were
  removed because they produced misleading failures on Authentik-protected or
  non-ICMP-friendly routes.
- Cloudflare and Tailscale are rendered as link-plus-status cards
  rather than widgets because that has been materially more reliable in this
  bootstrap flow than forcing those widget integrations.
