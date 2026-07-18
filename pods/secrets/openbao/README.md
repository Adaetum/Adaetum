# OpenBao

OpenBao is the authority layer introduced after bootstrap. Manifests and init
artifacts live here so Argo CD can deploy and configure the service. Bootstrap
may create temporary Kubernetes Secrets so applications can start before
OpenBao exists; after Phase 40, those copies are consumers rather than sources
of truth.

## Structure

- `manifests/`: OpenBao StatefulSet and services (Raft storage).
- `init/`: One-time init script and instructions.
- `policies/`: Policy definitions for workloads.
- `config/`: Post-init configuration job (auth methods, roles, policies).
- `../openbao-sync/`: the cluster store used to reconcile approved OpenBao
  values into the Kubernetes Secret names expected by existing applications.

## Notes

- Init output is stored locally on the first server.
- Unseal keys are human-held offline material.
- The first config Job uses `openbao-bootstrap-token`; later runs authenticate
  as the scoped `openbao/openbao` Kubernetes service account.
- The OpenBao UI is enabled and exposed through the standard routed UI model:
  `openbao.<domain>.local` and `openbao.<domain>` go through nginx and are
  expected to be protected by Authentik like the other operator-facing UIs.

## Secret ownership and rotation

The steady-state flow is:

```text
OpenBao KV -> External Secrets Operator -> Kubernetes Secret -> workload
                                                       |
                                                       +-> Reloader rollout
```

External Secrets polls OpenBao once per minute. Workloads that read credentials
from environment variables opt into Reloader, which performs a normal rolling
restart when the synchronized Kubernetes Secret changes. Kubernetes Secrets are
delivery copies: editing one directly is temporary and will be overwritten by
the next reconciliation.

Bootstrap is allowed to seed a missing application path once. Rerunning Phase
40, 50, or 60 does not overwrite fields that already exist under
`secret/apps/*` with node-local recovery material. Migration-gated database or
encryption values continue using their active Kubernetes copy until their
coordinator or documented migration explicitly promotes the desired OpenBao
version.

Workload delivery is continuously reconciled as follows:

| OpenBao field | Kubernetes consumer | Rotation behavior |
| --- | --- | --- |
| `secret/apps/ingress/external-dns:api_token` | `ingress/external-dns-cloudflare` | Issue a replacement at Cloudflare, patch OpenBao, then External Secrets updates the Secret and Reloader restarts external-dns. |
| `secret/apps/cloudflared/tunnel:token` | `cloudflared/cloudflared-tunnel` | Issue or retrieve a valid tunnel token, patch OpenBao, then External Secrets updates the Secret and Reloader restarts cloudflared. |
| `secret/apps/argocd/repository` | Argo CD repository and repo-creds Secrets | Replace the Gitea credential, patch OpenBao, and Argo CD observes the synchronized repository Secret. |
| `secret/apps/argocd/admin:password` | Argo CD's `argocd-secret` administrator hash | External Secrets updates an isolated plaintext delivery copy. A scoped CronJob detects a changed password, generates the bcrypt value Argo CD requires, and patches only the administrator hash and modification time. Existing sessions are revoked by Argo CD. |
| `secret/apps/argocd/runtime:server_secret_key` | `argocd/argocd-secret:server.secretkey` | External Secrets merges only the session-signing field and Reloader restarts the Argo CD server. Rotation deliberately revokes existing UI/API sessions without changing repository or application configuration. |
| `secret/apps/argocd/runtime:redis_password` | `argocd/argocd-redis:auth` | The chart's random secret-init Job is disabled. External Secrets updates the cache credential and Reloader restarts Redis plus every Argo CD Deployment and StatefulSet that can consume it. Redis is an ephemeral cache, so convergence may cause a brief control-plane retry window but does not require configuration or data migration. |
| `secret/apps/homepage/widgets` | `homepage/homepage-widget-secrets` | Replace or mint the configured Argo CD or Gitea widget token, patch OpenBao, and Reloader restarts Homepage. Authentik, Cloudflare, and Tailscale are links only and receive no credential. Gitea's token registry must confirm the exact required read scopes; after promotion, late reconciliation revokes superseded `homepage-widget*` tokens by ID. |
| `secret/apps/homepage/grafana` | Desired `observability/homepage-grafana-desired`, Grafana's dedicated `homepage` Viewer, then active `homepage/homepage-grafana` | A scoped coordinator creates or updates the Viewer in Grafana, enforces its Viewer role, validates the desired login, and only then promotes Homepage's delivery copy. Homepage never receives the Grafana administrator password. |
| `secret/apps/observability/apprise:apprise_yml` | `observability/apprise-config` | Patch the complete Apprise YAML in OpenBao; Reloader restarts Apprise with the synchronized file. |
| `secret/apps/ansible/tailscale` | `ansible/tailscale-user-sync` | Create the replacement OAuth client in Tailscale, patch both values in OpenBao, and Reloader restarts the day-two runner with only that scoped delivery Secret mounted. |
| `secret/apps/gitea/admin` | `gitea/gitea-admin-secret` | External Secrets updates the delivery copy, Reloader starts a new pod, and Gitea's native `keepUpdated` startup mode reconciles the internal admin account. |
| `secret/apps/gitea/actions-runner:token` | `gitea/gitea-actions-runner` | Request a replacement registration token from Gitea, patch OpenBao, and Reloader restarts the runner with the synchronized copy. A random KV value is not a valid registration token. |
| `secret/apps/gitea/registry` | `ansible/gitea-registry-creds` | Issue a replacement Gitea token, patch its host, username, and token fields in OpenBao, and External Secrets rebuilds the Docker config consumed by the day-two runner. |
| `secret/apps/gitea/push-mirror` | `gitea/gitea-push-mirror`, projected read-only into the Gitea container | Issue a replacement GitHub PAT with access to the private recovery repository, then patch `remote_url`, `username`, and `token` in OpenBao. The repository hook reads the projected files on every push, so the next mirror operation uses the replacement without restarting Gitea or rewriting its PVC. Bootstrap removes credential files created by the legacy hook implementation. |
| `secret/apps/gitea/runtime` | `gitea/gitea-runtime` | `internal_token` and `jwt_secret` are ordinary runtime signing material. External Secrets updates the delivery copy and Reloader restarts Gitea. Existing OAuth/LFS tokens may be invalidated, but persisted configuration does not need to be rebuilt. |
| `secret/apps/gitea/encryption:secret_key` | `gitea/gitea-encryption` | Migration-gated. This key protects persisted values such as 2FA secrets. External Secrets uses `OnChange`; migrate encrypted data before deliberately promoting a new version. |
| `secret/apps/gitea/postgresql` | Desired `gitea/gitea-postgresql-desired`, then active `gitea/gitea-postgresql` | A scoped coordinator changes the PostgreSQL superuser and Gitea roles in one transaction, promotes the active delivery Secret, and only then lets Reloader restart Gitea. A retry recognizes a committed database change even if delivery promotion was interrupted. |
| `secret/apps/authentik/admin` | `authentik/authentik-admin` | External Secrets updates the isolated admin copy, Reloader starts the server, and Authentik's lifecycle hook reconciles its internal administrator before the pod becomes ready. |
| `secret/apps/authentik/postgresql` | Desired `authentik/authentik-postgresql-desired`, then active `authentik/authentik-postgresql` | A scoped coordinator changes the PostgreSQL superuser and Authentik roles in one transaction, promotes the active delivery Secret, and only then lets Reloader restart the server and worker. |
| `secret/apps/authentik/encryption:secret_key` | `authentik/authentik-encryption` | For the pinned post-2023.6 Authentik release this is session-signing material, not a stable-user-ID key. External Secrets updates it and Reloader restarts the server and worker. Existing sessions are intentionally invalidated; persisted users and configuration remain valid. |
| `secret/apps/observability/grafana` | `observability/grafana-admin` | External Secrets updates the delivery copy, Reloader starts a new pod, and the startup wrapper resets Grafana's database-backed admin password before launching the server. |
| `secret/apps/observability/grafana:secret_key` | `observability/grafana-encryption` | Migration-gated. This is Grafana's key-encryption key for persisted datasource and alerting secrets. Rotate Grafana data keys normally; re-encrypt those keys before deliberately promoting a replacement `secret_key`. |
| `secret/apps/rancher/admin:bootstrap_password` | Desired `cattle-system/rancher-admin-desired`, then Rancher's local administrator and `bootstrap-secret` recovery copy | A scoped reconciler verifies the current login, submits Rancher's native `PasswordChangeRequest`, verifies the new login, and only then promotes the recovery copy. An interrupted run recognizes an already-active desired password. |

Provider-issued credentials are not arbitrary strings: Cloudflare must issue
the replacement before OpenBao is updated. OpenBao is authoritative for which
issued credential the cluster uses, but it cannot mint or revoke a provider's
credential by changing a KV value.

Not every Kubernetes Secret belongs in OpenBao:

- Kubernetes service-account tokens remain Kubernetes-owned and rotate through
  the TokenRequest/service-account mechanisms.
- Application encryption keys and database passwords require a coordinated
  application/data-store rotation. Blindly changing them and restarting pods
  can make stored data unreadable or lock applications out of their database.
- Values that are configuration rather than credentials should be ConfigMaps,
  even if an upstream chart currently packages configuration or init scripts
  in a resource whose Kubernetes kind is `Secret`.

Recovery roots are intentionally outside the store they recover:

- OpenBao unseal keys and the initial root token are exported only in the
  encrypted emergency bundle and then removed from the node and Kubernetes.
- The emergency-bundle passphrase cannot live only inside OpenBao, because it
  is required when OpenBao itself must be restored.
- R2 upload credentials, GitHub App private keys, and workflow dispatch
  credentials remain provider-issued recovery-plane inputs delivered through
  the private GitHub environment or a supported OS credential store. They must
  work when neither Kubernetes nor OpenBao exists, so mirroring them into
  workload KV would not replace their out-of-band authority.
- The RKE2 server token is retained in encrypted recovery material, but the
  running RKE2 cluster owns its live value. Rotate it with RKE2's native token
  rotation and coordinated server procedure, then refresh the recovery copy;
  never treat a `secret/bootstrap/platform` edit as a live token rotation.
- The Tailscale auth key embedded for first-node enrollment is a one-day,
  non-reusable installer credential. The OAuth client under
  `secret/apps/ansible/tailscale` is the durable application credential used to
  mint later enrollment keys.
- Kubernetes service-account tokens and controller-generated TLS private keys
  remain with Kubernetes and their owning controllers. They are identities or
  controller state, not application configuration to mirror into KV.

Phase 99 recursively discovers and encrypts every `secret/apps/*` leaf before
it removes node-local bootstrap files. It refuses the burn if the inventory or
any workload export fails, so a value rotated only in OpenBao is still present
in recovery material. Successful break-glass and join-node bundle runs also
remove the first-boot environment files, rendered bootstrap scripts, and
installed kickstart copies that can contain delivery credentials. These are
first-boot transport, not durable secret stores. They remain only when
bootstrap fails and needs its explicit resume path.

Gitea's global encryption root and Grafana's key-encryption key still need
application-specific migrations. Their delivery Secrets are OpenBao-owned, but
changing either credential also has to update stored data. Until that migration
is complete, do not describe those two fields as freely rotatable. Authentik's
pinned post-2023.6 signing key is different and can rotate at the cost of
invalidating active sessions.

Argo CD's controller-made TLS keys remain runtime state owned by Kubernetes and
Argo CD. Its Redis password is different: it is arbitrary authentication for an
ephemeral cache, so OpenBao owns it and a rotation restarts the cache and all
clients. The session-signing key is also OpenBao-owned; rotating it intentionally
logs users out.

To rotate a supported field, authenticate to OpenBao and patch only that field:

```bash
bao kv patch secret/apps/ingress/external-dns api_token='<issued-token>'
```

Then verify the `ExternalSecret` is `Ready` and the consuming Deployment has
completed its rollout. Do not put the value in Git, `platform.yaml`, or an Argo
CD manifest.

## Bootstrap token secret

Phase 40 creates this temporary control credential after initialization. Phase
99 deletes the Kubernetes copy after the encrypted recovery export succeeds.
The post-bootstrap config Job then uses its scoped Kubernetes identity. For a
manual recovery, create the temporary Secret with:

```bash
kubectl -n openbao create secret generic openbao-bootstrap-token \
  --from-literal=token=<OPENBAO_ROOT_TOKEN>
```
