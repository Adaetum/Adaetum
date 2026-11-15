# Part 2: How Adaetum does it (draft, copy first)

Part 1 (shots 1-25, built) ends on "your weekends are yours, and so is your money." Part 2 starts right after it, in the same film, same temple, same narrator, same acolytes and shrines.
Same rules as before: about 13 words per shot, no text in the 3D, facts only from the Adaetum docs, honest about what is not built.
Where the old storyboard.md Acts 4-13 overlap with this, this file wins. Security, restoring, and the app deep dive are folded in below.

Format for any "how" or "what if" section: the industry-standard way first, then the Adaetum way. Left of frame is the usual road, right is Adaetum's.

## Status
Built in 3D, with narration and objectives: Chapter A (3 shots, 26-28), Chapter B (34 shots, 29-62), Chapter C (a lead-in plus 17 scenarios x 3, plus a second removal option, shots 63-115).
Chapter E built (22 shots, 116-137): a lead-in, task init, the three providers, credentials, USB and first boot. The failed-first-boot line was cut.
Chapter D rebuilt as pro and con per option (14 shots with the intro, the choice and the close, 138-151). The RKE2/k3s-alone, limits and outside-accounts shots were cut. Options: DIY, managed cloud, OpenShift, Talos with Omni, Adaetum. Adaetum's con is: no support contract, community support only.
The finale (shots 150 and 151) is one continuous scene and camera move across both shots.
All chapters are built. Total film: 151 shots, about 12.6 minutes.

## Chapter A: The turn (3 shots)
A1. So how does Adaetum pull this off?
A2. It invents nothing. It wires proven tools together, and runs them for you.
A3. Here is what each one does, and why it earned its place.

## Chapter B: The team. Each tool, why it was chosen, what Adaetum adds
One pair of shots per tool, or one shot per group (see "Scale" at the end). Visual: a council of relics on altars, each lighting up as it is named.
Columns: job | why this one | what Adaetum adds | where the oracle (AI) helps | usual alternative

| Tool | Job | Why chosen for this use | What Adaetum adds | Oracle help | Usual alternative |
|---|---|---|---|---|---|
| Rocky Linux | The host OS | Stable, enterprise-grade, familiar, a normal mutable Linux you can SSH into | Installer media, unattended install | Launch playbooks (rehearsal first) | Ubuntu, RHEL, an immutable OS like Talos |
| Ansible | Keep every host configured the same | Standard, readable, agentless | A packaged runner that reconciles hosts on a schedule | Launch catalogued playbooks | Hand setup, Puppet, Chef |
| RKE2 | Kubernetes | Hardened to CIS benchmarks, FIPS-capable, one binary | Installs and wires it | Kubernetes connection to inspect | k3s, kubeadm, managed (EKS/GKE/AKS), OpenShift |
| Longhorn | Replicated storage | Built for small clusters, no separate storage system | Pre-installed and sized | Storage score, proposes growing a volume | Ceph/Rook, cloud disks, NFS |
| cert-manager | Certificates | Standard, renews on its own | Wired to ingress and internal CA | Lists certificate health | Manual renewals, Let's Encrypt scripts |
| Gitea | Source of truth in the cluster | Small, self-hosted Git | Seeded from your repo; mirrors out to GitHub | Gitea connection to read and propose | GitHub/GitLab only |
| Argo CD | Make the cluster match Git | The standard GitOps tool | Pointed at Gitea from the start | Explains why an app is out of sync | Flux, hand-applied manifests |
| OpenBao | Secrets vault | Open-source Vault fork, Raft storage, auto-unseal | Unseals itself; feeds apps only their secrets | Credentials minted and rotated for AI tools | Vault, cloud secret managers, files |
| External Secrets, CSI, Reloader | Deliver and refresh secrets | Standard Kubernetes pattern | Wired to OpenBao | None claimed | Sealed Secrets, SOPS |
| Authentik | Single sign-on | Full identity provider you host | Configured from files in Git | None claimed | Keycloak, Okta, per-app logins |
| Kubewarden, Kubescape | Policy and scans | Admission control as code | Starts in monitor mode; "latest" images refused | None claimed | OPA/Gatekeeper, Kyverno, scanners |
| Tailscale | Private network | WireGuard-based, easy, SSH included | Scoped keys, tag policy, node join | None claimed | VPN, bastion hosts, open ports |
| ingress-nginx, external-dns, kube-vip | Front door, DNS records, floating address | Standard Kubernetes parts | Wired together | None claimed | Cloud load balancers, manual DNS |
| Cloudflare Tunnel | Public access without open ports | Outbound-only connection | Built during setup | None claimed | Open ports, reverse proxy on a VPS |
| Prometheus, Grafana, Alertmanager, ntfy | Metrics, dashboards, alerts, phone pings | The standard monitoring stack | Pre-wired with maintenance alerts | Grafana connection to read dashboards | Datadog, cloud monitors |
| Kured, dnf-automatic, autoscalers | Patching and sizing | One reboot at a time, narrow owners | Policy in platform.yaml | Storage and sizing scores with suggestions | Manual patch nights |
| Host agent and web app | See and act in one place | Runs outside Kubernetes so it works when the cluster is down | Overview, Workloads, Cluster, Find apps, Automation, Security | The assistant: explains, proposes, asks approval | A pile of separate consoles |

## Chapter C: What happens when... (each: trigger, the usual way, the Adaetum way)
C1. A machine dies. Usual: a 3 a.m. page and a manual failover. Adaetum: apps restart on the others and the floating address moves. Caveat shot: one machine means downtime on reboot.
C2. A disk fails. Usual: restore from the last backup. Adaetum: Longhorn keeps copies on other machines. Not a backup.
C3. An app crashes. Usual: someone notices. Adaetum: it restarts, Prometheus and Alertmanager tell your phone through ntfy.
C4. A bad change goes out. Usual: change it live and hope. Adaetum: it is a Git commit, so revert it. Policies start in monitor mode.
C5. Someone edits a machine by hand. Usual: drift, forever. Adaetum: Ansible and Argo CD correct it.
C6. A certificate is about to expire. Usual: an outage on a Sunday. Adaetum: cert-manager renews it, 30 days early for the internal one.
C7. A password or key leaks. Usual: hunt through files and chats. Adaetum: rotate it in the vault and the apps reload. Keys are scoped and short-lived. [rotation is partly built]
C8. Updates are due. Usual: patch night. Adaetum: updates daily, reboots one machine at a time in a 3-6 a.m. window, blocked while an alert fires.
C9. The vault closes for maintenance. Usual: sealed until someone types keys. Adaetum: it unseals itself, at install and on every reboot.
C10. Add a machine. Usual: a weekend of setup. Adaetum: boot the installer; it joins. Servers share failover, workers add capacity.
C11. Remove a machine. Usual: pull the plug and hope. Adaetum, two options: drain it first so its apps move before it leaves, or just pull the plug and delete it because the apps relocate on their own. [option two is the maintainer's statement; not found in docs]
C12. An attacker probes. Usual: open ports and shared passwords. Adaetum: no open ports, one sign-in, policies, recorded access.
C13. The whole cluster is lost. Usual: rebuild from memory. Adaetum: rebuild from the plan and the recovery kit. Data backups are coming.
C14. A provider has an outage. Adaetum keeps running locally, with no cloud dependency at runtime, and stays reachable over the tailnet. [caveat: if Tailscale itself or Cloudflare is the provider that fails, private access or public apps are affected; the shot says a generic cloud provider]
C15. An app gets busy. Usual: buy a bigger box. Adaetum: adds copies, removes them when the rush ends. [Horizontal Pod Autoscaler: at least two replicas, scales up immediately, waits 15 minutes before removing capacity. Initially applies to an allowlist (Cloudflared, Apprise) per pods/operations/workload-autosizing/README.md]
C16. An app needs more room. Usual: restart it bigger. Adaetum: resizes memory in place without evicting. [Vertical Pod Autoscaler owns only the main container's memory request, in-place resize, same allowlist]
C17. Limits go stale. Usual: guessed limits. Adaetum: records usage; the oracle recommends raising or lowering limits. [Goldilocks is a read-only view of VPA recommendations. The agent keeps 14 days of hourly usage; shrink suggestions wait for about two days of data; the oracle proposes, you approve; Git stays the policy source]

## Chapter D: Compared with the industry standard (honest)
Sources: README comparison table and docs/comparison.md. Show each road's strengths first. Never claim Adaetum beats a paid vendor on support or scale.
D1. There are several well-worn roads to a cluster. Here is how they compare.
D2. Do it yourself with kubeadm, Terraform and Ansible. Total control, and total assembly.
D3. A managed cloud cluster. Easy start, and you rent forever.
D4. OpenShift. Mature, supported, certified, and heavy. It's a commercial platform.
D5. Talos with Omni. An immutable, API-driven system with a tiny attack surface and no SSH.
D6. RKE2 or k3s alone. A fine cluster, but secrets, identity, policy and recovery are on you.
D7. Adaetum: a normal Linux host, a full integrated stack, your configuration in a private repository you own.
D8. What it does not do well yet: it's early, one OS (Rocky 10), one node at install, no managed upgrades, a beta web app, and three outside accounts required.
D9. Choose the road that fits you. If you need a support contract, buy one.

## Chapter E: How it starts: task init and first boot
E-init (one setup tool, eight steps, on your computer). Source: setup.md, docs/diagrams/installer.md.
E1. On your computer, you run one command: task init. It walks eight steps.
E2. Step one: a private recovery repository on GitHub. Never a fork, and never public.
E3. Step two: it signs into Cloudflare and Tailscale and creates narrow, scoped keys.
E4. Step three: you choose a domain and a tailnet. It writes them to one public file, with no secrets.
E5. Step four: it finds and verifies the Rocky Linux installer.
E6. Steps five to eight: it validates, publishes, builds the installer, and checks the delivery path.
E-providers (why each outside account is there). Source: docs/integrations.md.
E7. GitHub holds your recovery repository, runs the build workflows, and keeps the backup copy.
E8. Cloudflare delivers the first-boot files, owns DNS, and builds the tunnel for public apps.
E9. Tailscale is the private network, the join path for machines, and the SSH route.
E10. Each uses its own credential: a setup token, a long-lived scoped client, and a short-lived join key. [reconcile one hour vs one day]
E11. Secrets reach tools through standard input, a temporary file, or the environment, never a command line.
E-first-boot (the machine side). Source: docs/diagrams/first-boot.md. The design is being reworked, so say "today".
E12. Write the installer to USB, boot a blank machine, and walk away.
E13. After the first reboot, it waits for a clock and a network, then joins your private network.
E14. It checks for an existing cluster. If one exists, it joins. If not, it starts one, and it never starts a second.
E15. It downloads the build bundle, and works through nine numbered phases (10, 20, 30, 40, 50, 60, 70, 90, 99).
E16. Phases build Kubernetes, storage, certificates and ingress, then start the vault.
E17. Secrets move from temporary files into the vault. After that, the vault is the authority.
E18. Argo CD and Gitea are installed, and Argo CD starts following Gitea.
E19. The cluster proves it can run from Git before it carries on.
E20. Last, it exports an encrypted recovery kit, and destroys its own temporary authority.
E21. (cut by the maintainer: no line about a failed first boot)

## Chapter F: Close
F1. Standard parts, a guided start, and a plan you can always rebuild from.
F2. Complexity goes down. Control does not.

## Open items to confirm before building
1. Tailscale join key: docs/diagrams/first-boot.md says one-time, one hour. docs/integrations.md says one-day, non-reusable. Confirm which is current.
2. How Adaetum treats regional and global failover (it is not in the docs). Do not claim it.
3. Node removal procedure: not found in the docs I read.
4. Credential rotation: partly built, roadmap says so. Keep C7 hedged.
5. Backups: roadmap only. Keep "coming".
6. First boot is being rebuilt on a branch. Say "today", and keep it high level.
7. Secret scanning in CI (Gitleaks) is not built. Do not claim.

## Scale
Rough size, at one shot per line above (5.2 s each, the H3 clip length):
- A 3, B 17 tools x 2 = 34 (or 7 group shots), C 14 x 3 = 42, D 9, E 21, F 2. About 110 shots, 9 to 10 minutes.
- Short cut: B as 7 group shots, C as 14 x 2 = 28. About 70 shots, 6 minutes.
## Build notes
- Same workspace, same player, appended after shot 25. Narration and objectives continue in the same JSON.
- New 3D needed: the council of relics, scenario triggers (machine dies, disk, certificate, leak, unseal), side-by-side "usual road" vs "Adaetum road" stagecraft, three rival sanctuaries for the comparison, the task init journey, USB installer, ten-rung ladder for the phases, recovery kit chest, and the three providers as distant temples (GitHub, Cloudflare, Tailscale) with scoped keys.
- The player should get a part filter so Part 2 can be scrubbed on its own.
