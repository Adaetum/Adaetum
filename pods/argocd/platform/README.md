# Platform Argo Apps

This folder holds platform-level Argo CD Applications for the bootstrap flow.

## Files

- `pre-openbao/`: applications needed before OpenBao is the authority.
- `post-openbao/`: applications applied after OpenBao is initialized.
- `post-openbao/application.yaml`: manual Application manifest (apply after OpenBao is live).

## Switch to post-OpenBao

1) Initialize and unseal OpenBao.
2) Create `openbao-bootstrap-token` secret in `openbao` namespace.
3) Enable the post-OpenBao app:

```bash
kubectl -n argocd apply -f pods/argocd/platform/post-openbao/application.yaml
```

4) Optionally disable the pre-OpenBao app:

```bash
kubectl -n argocd delete application platform-pre-openbao
```
