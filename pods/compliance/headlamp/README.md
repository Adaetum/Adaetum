## Headlamp

This cluster installs Headlamp through the upstream Helm chart and uses the
Headlamp plugin manager to install the Kubescape UI plugin.

What it does
- Provides an in-cluster web UI for browsing Kubernetes resources.
- Adds the Kubescape Headlamp plugin so compliance, vulnerability, and network
  findings from the Kubescape operator are visible in a supported UI.
- Leaves ingress disabled in the chart because this repo routes web apps
  through the shared nginx ingress manifests in `pods/ingress/nginx-routing`.

Access
- Public host: `headlamp.example.services`
- Internal host: `headlamp.example.local`
- Authentication is token-based unless you later wire an OIDC provider in
  front of it.
- Bootstrap exports a token for the chart-owned `headlamp` service account, stores it in the
  local bootstrap secret set, and persists it into OpenBao so it is included in
  exported recovery materials.

Notes
- The Argo CD app definition lives in [`../headlamp.app.yaml`](../headlamp.app.yaml).
- Kubescape documents Headlamp as the supported UI path for in-cluster access.
