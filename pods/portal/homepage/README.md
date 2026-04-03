## Homepage

This app provides a baseline cluster portal with curated internal and public
links plus built-in health indicators.

What it does
- Exposes a simple service catalog for the known cluster UIs and routes.
- Uses Homepage's `siteMonitor` feature for route status.
- Uses native Homepage widgets where supported. The current setup forces the
  stable in-cluster widgets on for Argo CD, Gitea, Grafana, and Prometheus.
- Provides a low-friction landing page for users before they know which UI or
  route they need.

Notes
- Internal and public reachability are authored as separate cards, but the
  browser now hides the opposite-scope cards at runtime. Users on `.local`
  only see the internal cards, and users on `.cloud` only see the public cards.
- Homepage monitors from the cluster, not from each user's browser, so this is
  cluster-vantage health rather than true end-user ISP/LAN vantage.
- `config/bookmarks.yaml` is intentionally empty so Homepage does not render its
  stock Developer/Social/Entertainment bookmark groups.
- Widget credentials are rendered from the `homepage-widget-secrets` Secret.
  Phase 50 only seeds best-effort values and operator-provided inputs there.
  Phase 90 is the authoritative finalization pass: it validates live Argo CD,
  Authentik, Gitea, and Grafana credentials, rebuilds `homepage-widget-secrets`
  from those validated values plus stable operator-provided Cloudflare and
  Tailscale values, and persists the final non-empty results into
  OpenBao `secret/bootstrap/platform`.
- Route status is intentionally `siteMonitor`-only. The old `ping` checks were
  removed because they produced misleading failures on Authentik-protected or
  non-ICMP-friendly routes.
- Authentik, Cloudflare, and Tailscale are rendered as link-plus-status cards
  rather than widgets because that has been materially more reliable in this
  bootstrap flow than forcing those widget integrations.
