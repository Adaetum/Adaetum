# OpenBao Initialization

This step is one-time. It establishes OpenBao as the authority. Shamir unseal
keys or Transit recovery keys are human-held and never stored in Git.

## Initialize

Run on the first server (or any operator workstation with kubectl):

```bash
pods/secrets/openbao/init/init-openbao.sh
```

This writes init output only to `/var/lib/bootstrap-secrets/openbao-init.json`.
The initializer must never print unseal material because bootstrap logs can be
collected for recovery diagnostics.

## Seal behavior

When `openbao-transit-seal` is absent, OpenBao uses Shamir and must be manually
unsealed after every process restart:

Unseal with at least the threshold number of keys:

```bash
kubectl -n openbao exec -it openbao-0 -- bao operator unseal
```

Repeat until the node is unsealed.

When platform bootstrap creates `openbao-transit-seal`, OpenBao initializes
with an external Transit seal and automatically unseals after restarts. The
Transit endpoint must be a separate OpenBao service that remains available
while this cluster is sealed.

## Migrate an existing Shamir installation

First configure the external Transit values in `.env` and rerun platform
bootstrap so the `openbao-transit-seal` Secret exists. Restore
`openbao-init.json` from the encrypted emergency kit if Phase 99 has already
removed it, then run on a server node:

```bash
sudo OPENBAO_TRANSIT_SEAL_MIGRATE=1 \
  KUBECTL_BIN=/var/lib/rancher/rke2/bin/kubectl \
  KUBECONFIG=/etc/rancher/rke2/rke2.yaml \
  pods/secrets/openbao/init/migrate-to-transit-seal.sh
```

The migration helper refuses implicit execution, saves a pre-migration Raft
snapshot, restarts OpenBao, submits three Shamir shares with the migration flag,
and verifies that the resulting seal type is `transit`. Do not remove the
external Transit key or its token after migration; recovery shares cannot
decrypt the OpenBao root key without that service.

## Post-init

- Store Shamir unseal keys or Transit recovery keys offline.
- Store the root token offline or in an approved temporary store.
- Apply post-openbao config (policies/auth/roles) via Argo CD.
