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
| Private-repository recovery and clean-cluster rebuild drill | Required | Required |
| Documentation, links, and support matrix review | Required | Required |
| Ubuntu experimental path | Not required | Required before stable support claim |

The release manager records command output, sanitized logs, relevant commits,
and known limitations in the GitHub release issue. No evidence may contain
secrets or recovery material.
