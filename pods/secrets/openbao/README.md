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

ESO polls OpenBao only for Kubernetes API consumers that cannot mount a CSI
volume. Runtime workloads authenticate their dedicated service accounts to
OpenBao through the CSI provider at pod creation. A chart that only accepts
environment variables may use CSI's pod-lifecycle synchronized delivery Secret;
it is still created from that authenticated CSI mount, not by ESO or bootstrap.
Editing any delivery copy directly is temporary and unsupported.

Bootstrap is allowed to seed a missing application path once. Rerunning Phase
40, 50, or 60 does not overwrite fields that already exist under
`secret/apps/*` with node-local recovery material. Migration-gated database or
encryption values continue using their active Kubernetes copy until their
coordinator or documented migration explicitly promotes the desired OpenBao
version.

The first control-pair boot is the sole structural exception: Argo's repository
credential, the initial actions-runner token, and the runner image-pull Secret
may be created once only when their ESO resources do not exist yet—those
resources are needed to install ESO itself. The bootstrap scripts mark this
bridge explicitly and switch to ESO waiting as soon as the resource exists;
they never use it as a rerun fallback.

For a deliberate runtime credential rotation, run the normal Phase 70
reconciliation with `BOOTSTRAP_RECONCILE_SECRET_ROTATION=1`. It restarts only
the named owning workloads, waits for their normal rollout, and proves that the
replacement pod published its matching `SecretProviderClassPodStatus`. It is
not a generic restart controller.

Workload delivery is continuously reconciled as follows:

| OpenBao field | Kubernetes consumer | Rotation behavior |
| --- | --- | --- |
| `secret/apps/ingress/external-dns:api_token` | CSI-mounted `ingress/external-dns-cloudflare` delivery copy | Issue a replacement at Cloudflare, update OpenBao, then restart the owning Deployment and verify the new pod's CSI status. |
| `secret/apps/cloudflared/tunnel:token` | CSI-mounted `cloudflared/cloudflared-tunnel` delivery copy | Issue or retrieve a valid tunnel token, update OpenBao, then restart the owning Deployment and verify the new pod's CSI status. |
| `secret/apps/argocd/repository` | Argo CD repository and repo-creds Secrets | Replace the Gitea credential, patch OpenBao, and Argo CD observes the synchronized repository Secret. |
| `secret/apps/argocd/admin:password` | Argo CD's `argocd-secret` administrator hash | External Secrets updates an isolated plaintext delivery copy. A scoped CronJob detects a changed password, generates the bcrypt value Argo CD requires, and patches only the administrator hash and modification time. Existing sessions are revoked by Argo CD. |
| `secret/apps/argocd/runtime:server_secret_key` | `argocd/argocd-secret:server.secretkey` | External Secrets merges only the session-signing field and Reloader restarts the Argo CD server. Rotation deliberately revokes existing UI/API sessions without changing repository or application configuration. |
| `secret/apps/argocd/runtime:redis_password` | `argocd/argocd-redis:auth` | The chart's random secret-init Job is disabled. External Secrets updates the cache credential and Reloader restarts Redis plus every Argo CD Deployment and StatefulSet that can consume it. Redis is an ephemeral cache, so convergence may cause a brief control-plane retry window but does not require configuration or data migration. |
| `secret/apps/homepage/widgets` | `homepage/homepage-widget-secrets` | Replace or mint the configured Argo CD or Gitea widget token, patch OpenBao, and Reloader restarts Homepage. Authentik, Cloudflare, and Tailscale are links only and receive no credential. Gitea's token registry must confirm the exact required read scopes; after promotion, late reconciliation revokes superseded `homepage-widget*` tokens by ID. |
| `secret/apps/homepage/grafana` | Desired `observability/homepage-grafana-desired`, Grafana's dedicated `homepage` Viewer, then active `homepage/homepage-grafana` | A scoped coordinator creates or updates the Viewer in Grafana, enforces its Viewer role, validates the desired login, and only then promotes Homepage's delivery copy. Homepage never receives the Grafana administrator password. |
| `secret/apps/observability/apprise:apprise_yml` | direct `observability/apprise-openbao` CSI file | Patch the complete Apprise YAML in OpenBao; the mounted file rotates and the owning rollout verifies a replacement mount. |
| `secret/apps/ansible/tailscale` | direct `ansible/ansible-runner-openbao` CSI files | Create the replacement OAuth client in Tailscale, patch both values in OpenBao, then restart the day-two runner and verify its CSI mount. |
| `secret/apps/gitea/admin` | CSI-mounted `gitea/gitea-admin-secret` delivery copy | CSI creates the chart-required copy during pod setup; Gitea's native `keepUpdated` startup mode reconciles the internal admin account after an owning rollout. |
| `secret/apps/gitea/actions-runner:token` | `gitea/gitea-actions-runner` | Request a replacement registration token from Gitea, patch OpenBao, and Reloader restarts the runner with the synchronized copy. A random KV value is not a valid registration token. |
| `secret/apps/gitea/registry` | `ansible/gitea-registry-creds` | Issue a replacement Gitea token, patch its host, username, and token fields in OpenBao, and External Secrets rebuilds the Docker config consumed by the day-two runner. |
| `secret/apps/gitea/push-mirror` | `gitea/gitea-push-mirror`, projected read-only into the Gitea container | Issue a replacement GitHub PAT with access to the private recovery repository, then patch `remote_url`, `username`, and `token` in OpenBao. The repository hook reads the projected files on every push, so the next mirror operation uses the replacement without restarting Gitea or rewriting its PVC. Bootstrap removes credential files created by the legacy hook implementation. |
| `secret/apps/gitea/runtime` | CSI-mounted `gitea/gitea-runtime` delivery copy | `internal_token` and `jwt_secret` are ordinary runtime signing material. An owning rollout after OpenBao rotation creates the current copy before Gitea starts. Existing OAuth/LFS tokens may be invalidated, but persisted configuration does not need to be rebuilt. |
| `secret/apps/gitea/encryption:secret_key` | CSI-mounted `gitea/gitea-encryption` delivery copy | Migration-gated. This key protects persisted values such as 2FA secrets; migrate encrypted data before deliberately promoting a replacement. |
| `secret/apps/gitea/postgresql` | Desired `gitea/gitea-postgresql-desired`, then active `gitea/gitea-postgresql` | A scoped coordinator changes the PostgreSQL superuser and Gitea roles in one transaction, promotes the active delivery Secret, and only then lets Reloader restart Gitea. A retry recognizes a committed database change even if delivery promotion was interrupted. |
| `secret/apps/authentik/admin` | CSI-mounted `authentik/authentik-admin` delivery copy | CSI creates the isolated admin copy during pod setup, and Authentik's lifecycle hook reconciles its internal administrator before the pod becomes ready. |
| `secret/apps/authentik/postgresql` | Desired `authentik/authentik-postgresql-desired`, then active `authentik/authentik-postgresql` | A scoped coordinator changes the PostgreSQL superuser and Authentik roles in one transaction, promotes the active delivery Secret, and only then lets Reloader restart the server and worker. |
| `secret/apps/authentik/encryption:secret_key` | CSI-mounted `authentik/authentik-encryption` delivery copy | For the pinned post-2023.6 Authentik release this is session-signing material, not a stable-user-ID key. An owning rollout creates the current copy before server and worker start. Existing sessions are intentionally invalidated; persisted users and configuration remain valid. |
| `secret/apps/observability/grafana` | direct `observability/grafana-openbao` CSI files; `grafana-admin` remains only for the Homepage Viewer controller | An owning rollout mounts the current admin/password/key files before Grafana starts; the scoped Viewer controller uses the ESO adapter only to call Grafana's API. |
| `secret/apps/observability/grafana:secret_key` | direct `observability/grafana-openbao` CSI file | Migration-gated. This is Grafana's key-encryption key for persisted datasource and alerting secrets. Rotate Grafana data keys normally; re-encrypt those keys before deliberately promoting a replacement `secret_key`. |
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
