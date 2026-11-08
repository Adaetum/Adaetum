# Adaetum cluster agent

The `adaetum-agent` application runs the cluster agent and its authentication
service together, with a separate embedding service. It starts with automatic
Argo CD sync disabled until release images have been published and pinned.

The service is cluster-internal. For initial access, an operator with cluster
access can run `kubectl -n adaetum port-forward service/adaetum-cluster-agent
8444:8444` and open `https://localhost:8444`. The service uses its own TLS
certificate. Obtain the one-time first-owner setup code from the auth container's
logs: `kubectl -n adaetum logs deployment/adaetum-cluster-agent -c auth`.
Treat that code and the emergency token on the persistent volume as credentials.

The cluster page can check for an agent release. An owner or administrator may
prepare a Gitea pull request that pins the agent, auth, and embedding images to
that release's digests; an operator must review and merge it. Host updates use
the existing Ansible launcher and its preview and confirmation. Ansible execution
is disabled until enabled in Security. When enabled, the agent can create jobs
using the privileged Ansible runner and should be protected accordingly.

The proposal credential has repository and issue write scopes on the Gitea
administrator account. It is stored separately in OpenBao, but those scopes are
not limited to a single repository. The agent's PVC contains its databases,
account state and recovery credentials; arrange persistent-volume backups before
relying on the deployment for recovery. The current deployment does not itself
configure those backups.

## Publishing a release

Run **Release Adaetum agent software** from the public repository's Actions tab,
choose the branch to release, and supply a new tag such as
`adaetum-v0.1.2-beta.1` for development or `adaetum-v0.1.2` for stable. Stable
releases must use the current main branch; beta releases are GitHub prereleases
and do not become latest. The selected commit supplies the binaries, images and
manifest. Only a manual start can publish a release, including when automatic
Actions are disabled.

The workflow uses the repository's `GITHUB_TOKEN`, even when a person starts it.
The three GHCR packages must grant this repository's Actions workflow write
access; public visibility is required for anonymous cluster pulls. GitHub may
create packages as private. Check each package's settings after its first
publication. The operator must pin the resulting image digests in the pod
kustomization and enable the application before deploying it.
