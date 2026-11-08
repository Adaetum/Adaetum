# MCP servers

Model Context Protocol servers for the products Adaetum already runs, so an AI
assistant can operate them. They are ordinary Argo CD apps in the `mcp` namespace,
independent of Adaetum's own agent: the agent is just one client (it connects over
HTTP and does not run or embed them).

| App | Server | Reaches | Caller must present |
| --- | --- | --- | --- |
| `kubernetes-mcp` | [containers/kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server) | the Kubernetes API, as the `kubernetes-mcp` service account | nothing (the network policy is the gate) |
| `argocd-mcp` | [argoproj-labs/mcp-for-argocd](https://github.com/argoproj-labs/mcp-for-argocd) | Argo CD, as its `mcp` account | `Authorization: Bearer <inbound_token>` |
| `grafana-mcp` | [grafana/mcp-grafana](https://github.com/grafana/mcp-grafana) | Grafana, as the `adaetum-mcp` service account | `Authorization: Bearer <inbound_token>` |
| `gitea-mcp` | [gitea/gitea-mcp](https://gitea.com/gitea/gitea-mcp) | Gitea, as the administrator account | a Gitea token (Gitea validates it) |

Each is reached inside the cluster at `http://<app>.mcp.svc.cluster.local`. The path
to the MCP endpoint (usually `/mcp`) is set by each server; see its own docs.

## Full access, on purpose

These run with everything enabled, including the tools that change things. What
keeps that safe is not a read-only switch but who can call them and what the
assistant does before it calls:

- **Network:** each server's `NetworkPolicy` admits only pods labelled
  `adaetum.io/mcp-client: "true"`. Nothing carries that label until a client is
  deployed, so nothing can reach them yet.
- **Caller credential:** Argo CD's and Grafana's servers also refuse a caller without
  its token.
- **Approval:** whatever connects to these servers decides whether a person approves a
  change first. Adaetum's own assistant asks before any call that is not known to be
  read-only.

`kubernetes-mcp` can read Secret contents and run commands in pods. Its role leaves
out only the verbs that raise privilege (`escalate`, `bind`, `impersonate`); see
`kubernetes-mcp/rbac.yaml` to narrow it.

## How they set themselves up

1. Argo CD's `mcp` account is declared in the `argocd-install` values
   (`accounts.mcp`, with the admin role).
2. `mcp-credentials` (a CronJob, every five minutes) reads each product's
   administrator login from OpenBao, has the product issue a credential for the MCP
   server, stores it at `secret/apps/mcp/<gitea|argocd|grafana>`, reads it back,
   and then revokes the ones it replaced. It also makes the caller tokens. With
   nothing wrong it only reads. See `credentials/mint-credentials.sh`.
3. Each server reads only its own path through the OpenBao CSI provider. A pod waits
   at container creation until its credential exists, then starts by itself.

Nothing here needs a person. Reading these values later (for example from Adaetum's
agent) is done from OpenBao at the same paths.

## Not verified

The images and their digests were looked up in their registries, but none of these
servers has been run for this change: their command-line flags are taken from each
project's documentation. If a server fails to start, check that against its
`--help`.
