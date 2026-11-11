# Adaetum cluster agent

The `adaetum-agent` application runs the cluster agent and its authentication
service together, with a separate embedding service. Argo CD syncs the application automatically using the image digests pinned in
its kustomization.

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
`adaetum-v0.1.2-beta.1` for the beta channel or `adaetum-v0.1.2` for stable.
Stable releases must use the current tip of the `main` branch and beta releases
the current tip of the `beta` branch (create `beta` first); beta releases are
GitHub prereleases and do not become latest. The selected commit supplies the binaries, images and
manifest. Only a manual start can publish a release, including when automatic
Actions are disabled.

The workflow uses the repository's `GITHUB_TOKEN`, even when a person starts it.
The three GHCR packages must grant this repository's Actions workflow write
access; public visibility is required for anonymous cluster pulls. GitHub may
create packages as private. Check each package's settings after its first
publication. Release updates can be proposed and reviewed through the agent before GitOps
applies the new image digests.

## Host installation

The Rocky installer media includes the pinned host-agent binary for its target
architecture. Kickstart installs it, creates a unique token under
`/etc/adaetum/agent-token`, and enables the service on every installed server.
The host agent starts independently of the cluster and can be managed later by
the `host-agent` Ansible playbook. An operator must still enroll each host with
the cluster agent using a one-time join token; installing the binary alone does
not enroll it or configure firewall access. See `adaetum/agent/docs/cluster.md`.
