# OpenBao Initialization

This step is manual and one-time. It establishes OpenBao as the authority.
Unseal keys are human-held and never stored in git.

## Initialize

Run on the first server (or any operator workstation with kubectl):

```bash
pods/secrets/openbao/init/init-openbao.sh
```

This writes init output only to `/var/lib/bootstrap-secrets/openbao-init.json`.
The initializer must never print unseal material because bootstrap logs can be
collected for recovery diagnostics.

## Unseal

Unseal with at least the threshold number of keys:

```bash
kubectl -n openbao exec -it openbao-0 -- bao operator unseal
```

Repeat until the node is unsealed.

## Post-init

- Store unseal keys offline (secure human custody).
- Store the root token offline or in an approved temporary store.
- Apply post-openbao config (policies/auth/roles) via Argo CD.
