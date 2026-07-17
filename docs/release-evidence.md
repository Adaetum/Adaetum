# Platform-contract release evidence

Each platform-contract release issue must link evidence for the following checks. Stable
release candidates may not replace an item with an assertion or an unreviewed
manual test.

| Evidence | Alpha | Stable |
| --- | --- | --- |
| Profile validation and generated-manifest render checks | Required | Required |
| Rocky 10 clean install | Required | Required |
| Default-profile bootstrap and recovery | Required | Required |
| Cloudflare, GitHub, and Tailscale integration validation | Required | Required |
| OpenBao-to-External-Secret convergence and workload rollout | Required | Required |
| Product-aware password/database rotation and interrupted-run recovery | Required | Required |
| Restart persistence after rotation, without bootstrap replay or configuration loss | Required | Required |
| Private-repository recovery and clean-cluster rebuild drill | Required | Required |
| Documentation, links, and support matrix review | Required | Required |
| Ubuntu experimental path | Not required | Required before stable support claim |

The release manager records command output, sanitized logs, relevant commits,
and known limitations in the GitHub release issue. No evidence may contain
secrets or recovery material.

Secret evidence must cover the behavior classes rather than only one easy
credential:

- one arbitrary stateless credential must converge from OpenBao through
  External Secrets and trigger the intended workload rollout;
- one provider-issued credential must be replaced at its provider, selected in
  OpenBao, and consumed successfully after reconciliation;
- one application-owned password and one database password must complete their
  product-aware desired-to-active promotion, including a safe retry after an
  intentionally interrupted run;
- the Gitea recovery-mirror token must change without writing a credential to
  the repository PVC, and a later mirror push must use the projected value;
- affected applications must survive a subsequent restart without replaying
  bootstrap or losing persisted users, repositories, dashboards, or other
  configuration; and
- Gitea and Grafana encryption roots must remain migration-gated. A release
  must not use a blind KV edit as evidence that these roots rotate safely.

Capture resource versions, ExternalSecret conditions, rollout completion, and
application-level success while redacting all values. A Kubernetes Secret
changing is delivery evidence, not proof that a backing application or
provider accepted the new credential.
