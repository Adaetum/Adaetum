# OpenBao

OpenBao is the authority layer introduced after bootstrap. Manifests and init
artifacts live here so Argo CD can deploy and configure the service.

## Structure

- `manifests/`: OpenBao StatefulSet and services (Raft storage).
- `init/`: One-time init script and instructions.
- `policies/`: Policy definitions for workloads.
- `config/`: Post-init configuration job (auth methods, roles, policies).

## Notes

- Init output is stored locally on the first server.
- Unseal keys are human-held offline material.
- The config job requires `openbao-bootstrap-token` to exist in-cluster.
- The OpenBao UI is enabled and exposed through the standard routed UI model:
  `openbao.<domain>.local` and `openbao.<domain>` go through nginx and are
  expected to be protected by Authentik like the other operator-facing UIs.

## Bootstrap token secret

Create the secret after initialization (manual step):

```bash
kubectl -n openbao create secret generic openbao-bootstrap-token \
  --from-literal=token=<OPENBAO_ROOT_TOKEN>
```
