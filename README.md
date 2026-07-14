# Adaetum

Adaetum is a security-first, self-hosted platform activation project for
homelab operators. It turns a fresh Rocky Linux 10 machine into an
RKE2-based cluster with GitOps, secrets, identity, recovery, and observability
integrated from the beginning.

Adaetum composes proven upstream products instead of building replacement
control planes: Argo CD, Gitea, OpenBao, Authentik, Headlamp, Homepage,
Prometheus, Grafana, and others remain the operator-facing tools.

## Fork-first ownership

Adaetum is designed to be forked. Your private fork is where you choose the
platform shape and build the break-glass bundle that instantiates a new
cluster. Bootstrap seeds that configuration into the cluster's Gitea instance;
from that point, the in-cluster Gitea repository is the authoritative
operational source of truth and Argo CD reconciles it. The private fork remains
an out-of-band configuration and recovery copy if the cluster is unavailable.
Adaetum does not promise an in-place upgrade API between major designs:
upstream breaking changes are adopted deliberately in your fork.

## Platform contract

[`platform.yaml`](platform.yaml) is Adaetum's single public, non-secret
configuration contract. It defines the fork-owned cluster shape and delivery
settings; the supported product stack is defined directly under [`pods/`](pods).
The generated `.env`,
`pods/cluster-config/cluster-config.env`, and rendered manifests are
implementation outputs; do not hand-edit them.

| Area | Support |
| --- | --- |
| Rocky Linux 10 installer | Stable target |
| Ubuntu 24.04 installer | Experimental, disabled |
| External bootstrap integrations | Cloudflare R2/edge, GitHub Actions, Tailscale |
| In-cluster product stack | Argo CD, Gitea, OpenBao, Authentik, Homepage, Headlamp, Prometheus, Grafana |

## Quick start

Fork the repository, then replace the deliberately safe defaults in
[`platform.yaml`](platform.yaml) with your domain, tailnet, repository, and
bootstrap-delivery settings. For the supported first-node flow, read
[setup.md](setup.md) and run:

```bash
task initialize
```

Validate and render the fork-owned profile after changing it:

```bash
task platform:validate
task platform:render
```

`task platform:setup` uses the fork's `platform.yaml` to render public platform
configuration while retaining `.env` only for runtime secrets.

## Documentation

- [Architecture and audit](docs/audit.md)
- [Release evidence](docs/release-evidence.md)
- [Versioning policy](docs/versioning.md)
- [External integration boundaries](docs/integrations.md)
- [Maintainer triage and release process](docs/maintainer-process.md)
- [Bootstrap and recovery guide](setup.md)
- [Public roadmap](TODO.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md) and [support policy](SUPPORT.md)

## Contributing

Contributions are welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md), use
the issue templates for proposals and bugs, and keep pull requests focused.
Please follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Adaetum is released under the [MIT License](LICENSE).
