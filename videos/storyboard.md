> Part 1 is built (shots 1-25) and considered complete as a reference. Later acts in this file are superseded by storyboard_part2.md where they overlap.

# Product demo: storyboard and draft copy

One film of 5.2 s shots (124 frames at 24 fps, the H3 clip length). About 13 words of narration per shot.
Facts come from the Adaetum README and docs. Shot numbers continue from the built shots 1-12.
Three messages the whole film keeps returning to:
1. Industry-standard parts, not a reinvention.
2. Setup and maintenance get simpler, including through an LLM.
3. You keep control. Complexity goes down, ownership does not.

## Act 1: The dream (built, shots 1-4)
1. You and a friend pay for storage, music, video, and AI tools.
2. Every month the bills come due, and at the end of the year, you own none of it.
3. So you decide to host it all yourselves, AI included.
4. Then the questions start. Networking, security, updates, backups.

## Act 2: Where could it live (built, shots 5-9)
5. A laptop at home is a start, until the power goes out.
6. Virtual machines share one host, so when it fails, they all go down.
7. A rented server survives your power cut, but it's still one machine. (built, line updated)
7b. And the bill never stops, and you can't outgrow the plot. (new line, needs a second shot or a longer shot 7)
8-pre. By hand, staying up is its own project: several machines, a load balancer, health checks, and replicated storage.
8-pre2. A failover you've never tested is only a prayer. (built; the 'break things on purpose' sentence was cut)
8-pre3. Then drift sets in. Quick fixes at 2 a.m. leave no two machines alike, and nobody remembers why.
8. Change one thing with no review and no rehearsal, and the tower falls. (built, line updated)
Note: drift and risky change are separate problems. Drift is the running system quietly diverging from what you think it is. Risky change is a deliberate edit that breaks something, with no review, no rehearsal, and no easy way back.
SSH note: the override runs one command at a time on a named server, not a full interactive terminal (an in-page terminal is not built). Keep the copy to 'fix a machine as if you were signed in' and avoid promising a shell session.
Visuals: tower collapse already fits risky change. Drift needs its own shot (machines slowly changing shape and colour while the plan stays put). Failover-by-hand needs a shot of assembling the parts.
9. Kubernetes solves all of this, but it was built for teams of engineers.

## Act 2b: What "staying up" really means (built, shots 13-17)
Place after Kubernetes (shot 9), before "What if a small cluster could look after itself".
A. Anything you only have one of is a single point of failure.
B. Two machines at your altar give you local high availability. If one fails, the other takes over. (places are chapels)
C. Machines at a friend's altar add regional high availability, so one chapel falling doesn't stop you.
D. Optional failover into AWS, Google, or Azure makes it global, so even losing both chapels doesn't stop you.
E. Each level adds cost and complexity, so choose what your apps need.
Note: these levels describe what you can build. Whether Adaetum itself manages the regional and global levels is not in the docs. [CONFIRM] before the film says Adaetum does it.
Note: VMs (shot 6) fail together because they share one host. A VPS (shot 7) fixes your house being the failure, not the machine being the failure.

## Act 3: The idea (built, shots 10-16). Numbers from Act 4 on shift by 4.
10. What if your machine god patched itself, healed itself, and kept to the plan?
11. It updates one machine at a time, so the rest keep serving. (built)
12. If an app fails, it restarts. If a machine fails, its apps move. (built)
13. A plan in Git says how things should look, and drift gets pulled back. (built)
14. One screen shows what's healthy, and an AI assistant helps fix what isn't. (built)
15. Plug in an installer, and a blank machine sets itself up and joins the cluster.
16. Now your apps just run, your weekends are yours, and so is your money.

## Act 4: Setting it up (specifics from setup.md and docs/diagrams/installer.md)
Visual idea: follow one blank machine from nothing to a working cluster. Reuse the friends from Act 1.
17. On your computer, one setup tool asks for two things: your domain and your private network.
18. It creates a private repository for the cluster's plan. Nothing secret is ever public.
19. It connects your DNS and your network, using narrow keys that can only do one job.
20. Then it builds an installer image and publishes what the first machine will need.
21. Write the image to a USB stick, boot a blank machine, and walk away.
22. On first boot, the machine joins your private network with a key that expires in an hour.
23. It checks for an existing cluster. If one exists, it joins. If not, it starts one.
24. It builds in order: Kubernetes, storage, the secret vault, then Git and Argo CD.
25. Secrets move into the vault, and the temporary copies are destroyed.
26. Last, it exports an encrypted recovery kit, so you can always rebuild.

## Act 5: What's inside, and why each part (new)
27. Adaetum isn't a new kind of software. It's proven open-source parts, wired together.
28. Rocky Linux is an ordinary server, and Ansible keeps every machine configured the same.
29. RKE2 is Kubernetes, hardened for security. Longhorn copies your data across machines.
30. Git holds the plan. Argo CD compares the plan with the cluster, and fixes the difference.
31. OpenBao holds secrets, and each app receives only the ones it needs.
32. Authentik is one front door for every tool you run.
33. Policy checks review each change, and Kubescape reports risks.
34. Prometheus and Grafana watch health. Alertmanager and ntfy tell you when it matters.
35. Every piece is standard. You can read it, change it, or swap it out.

## Act 6: Growing and failing over (new). Each is a pair: the usual way, then Adaetum.
36-37. Add a machine: hours of setup, vs boot the installer and it joins.
38. Server machines share failover. Worker machines only add capacity.
Pairs to add in Act 6, one for each problem raised in Act 2:
- Failover set up for you: server machines share it, and a floating address moves when one fails (usual way: build and wire it yourself).
- Failover tested: kill a machine on purpose, and watch the apps move (usual way: find out during a real outage).
- Drift corrected: Argo CD compares Git with the cluster and puts back anything that changed (usual way: machines quietly diverge).
- Safe change: edit the plan, review the diff, and revert if it goes wrong. Policies start in monitor mode, playbooks have a rehearsal (usual way: change it live and hope).
39-40. A machine dies: apps go down with it, vs they restart on the others.
41. Honest caveat: with one machine, a reboot stops everything. More machines, more availability.
42-43. Secrets: passwords in files and chats, vs one vault with scoped delivery.
44-45. Recovery: rebuild from memory, vs rebuild from your repository.

## Act 6b: High availability is not a backup (new, middle of the film)
Place right after the failover pairs in Act 6.
B1. High availability keeps you running. It doesn't keep your data safe from mistakes.
B2. If something deletes or corrupts your data, every live copy follows it.
B3. A backup is a separate, older copy, kept where the same failure can't reach it.
B4. The usual rule is three copies, on two kinds of storage, with one off site.
B5. Two numbers matter: how much data you can afford to lose, and how long you can be down.
B6. And a backup you've never restored is only a hope. Test it.

## Act 7: Keeping it running (new)
46. Updates install daily, and reboots happen in a quiet window, between 3 and 6 a.m.
47. Only one machine reboots at a time, and an active alert stops the next one.
48. Every change goes through Git: edit, review, commit, and the cluster follows.

## Act 8: What you see and touch (new). The agent and its web app.
49. A small program on every machine keeps working, even when the cluster is down.
50. Overview shows what's healthy and what needs attention, in order.
51. Each app gets a card and a setup score, with plain reasons for every lost point.
52. Find apps searches public charts and images, and drafts a change for review.
53. Automation runs ready-made playbooks, with a rehearsal first, and a record of every run.

## Act 9: Simpler with an LLM, and still yours (new)
54. You can appoint your own AI oracle. It explains problems in plain language, and proposes fixes.
55. The oracle asks for your approval before it changes anything. You choose which AI serves you.
56. Other AI tools can connect too, through standard connections, behind network rules and tokens.
57. Normally, changes go through reviewed playbooks, so they are rehearsed and recorded.
58. When a playbook isn't enough, you can turn on SSH, and fix a machine as if you were signed in to it.
59. It's off by default, it switches itself off after a time you choose, and each command is recorded before it runs.
59b. An assistant can use it too, but only with your approval, and a reason on the record.
59c. And a check in the repository fails the build if anyone adds a way around those rules.
60. Complexity goes down. Control does not.

## Act 10: Defending it against attackers (rewritten, was four lines)
Format: what an attacker tries, then what stands in the way. Occult framing: wards, seals, sentinels. Facts come from README, setup.md, docs/diagrams/first-boot.md, security-boundary.md, pods/compliance and pods/mcp.
Intro. If it's on the internet, someone will try to get in. Here is how it's defended.

Closing the doors
S1. Attackers start by looking for open doors. The first ward is having none: no ports open at home.
S2. Public apps arrive through a Cloudflare tunnel. Private tools are reachable only over Tailscale.
S3. Every tool sits behind one sign-in, so access is decided in one place.
Guarding the secrets
S4. Attackers hunt for passwords in files. Secrets live in a vault, never in the repository.
S5. Each app sees only its own secrets, and the vault unseals itself after a restart.
S6. Setup secrets are temporary. When bootstrap finishes, the local copies are destroyed.
S7. Machines join with scoped keys that expire in an hour, so a leaked key does little.
Hardening the ground
S8. The cluster is RKE2, hardened to security benchmarks from the start.
S9. Policies check every change before it's admitted. Unpinned "latest" images are refused.
S10. Scans look for weaknesses, so you hear about them before an attacker does.
S11. Updates install daily, and machines reboot one at a time, so known holes close quickly.
Limiting what any one hand can do
S12. The agent never runs raw commands on a server. It launches reviewed, recorded playbooks.
S13. Direct SSH is off by default, it expires on its own, and every command is recorded first.
S14. The assistant can propose, but it can't change anything without your approval.
S15. Tools used by AI sit behind network rules and tokens that Adaetum creates and replaces on a schedule.
If the worst happens
S16. If a machine is compromised, you don't repair it by hand. You rebuild it from the plan.
S17. Security is layers, not magic. Keep your accounts safe, and keep it updated.

Threats this covers, in plain words: exposed services, stolen or reused passwords, secrets leaked in files, unpatched software, risky images, over-powerful tools (including AI), and a compromised machine.
Not claimed, because the docs say it is not done or not proven:
- Per-person accounts everywhere: the agent still uses one shared access token, so records say "operator". Recovery login is in progress.
- Automated secret scanning in CI (Gitleaks) is on the roadmap, not built.
- Coordinated credential rotation is partly done; the full path is not proven.
- A single node still reboots with downtime; hardening does not change availability.
- Off-site backups are coming, not built (see Act 12).
Possible placement: move after Act 5 (the stack) if you want security before the side-by-sides.

## Act 11: How we work in the repo (new)
65. One public file describes the cluster, and holds no secrets.
66. Check, commit, push. The build runs what you pushed, not what's on your laptop.
67. After handoff, Gitea is the source of truth. GitHub is the backup copy.
68. If setup fails, fix it and rerun. If a machine dies, rebuild from the repository.

## Act 12: Restoring (new, near the end)
Lead with configuration as code. Backups are coming, so say that plainly, and never say "included" until built.
R1. Your whole platform is described as code, so anything can be set up again.
R2. The plan lives in your private repository, outside the cluster, so losing the cluster doesn't lose the plan.
R3. Lose a machine, and the cluster carries on. Lose all of them, and you rebuild.
R4. Boot the installer on new hardware, and the platform comes back exactly as planned.
R5. Secrets live in the vault, and an encrypted recovery kit is exported when setup finishes.
R6. Nightly off-site backups of your app data are coming next, so a rebuild brings your data back too.
R7. The goal is a rehearsed drill: rebuild, restore, and watch your data return.

## Act 13: The bundled apps. Why each was chosen, and how Adaetum and AI help (new)
Format for every app: shot A says what it is and why it fits this use case. Shot B says what Adaetum does so you don't, and where the assistant helps.
Rule: the assistant is opt-in, proposes changes, and waits for approval. In-cluster AI connections use scoped credentials that Adaetum mints and replaces on a schedule.
Intro. Every part here is a standard tool. Here is why each is on the team, and what we added.

Foundation
13.1A. Rocky Linux: a stable, enterprise-grade server you already know how to run.
13.1B. Ansible keeps every host the same. The assistant can launch playbooks, rehearsal first.
13.2A. RKE2: Kubernetes hardened for security, which suits a platform you expose to a network.
13.2B. Adaetum installs and wires it. The assistant can inspect the cluster through a Kubernetes connection.
13.3A. Longhorn: replicated storage for small clusters, with no separate storage system to run.
13.3B. Each app gets a storage score. The assistant can propose growing a volume that's filling up.
13.4A. cert-manager: certificates that issue and renew themselves.
13.4B. No expiry dates to remember. The agent lists every certificate and its health.

Plan and delivery
13.5A. Gitea: small, self-hosted Git that holds the cluster's plan after setup.
13.5B. It mirrors out to your private GitHub copy. An assistant can read and propose changes in it.
13.6A. Argo CD: the standard way to make a cluster match what's in Git.
13.6B. It's wired to Gitea from the start. The assistant can explain why an app is out of sync.

Secrets and identity
13.7A. OpenBao: an open-source secrets vault, so no password sits in a file.
13.7B. It unseals itself after restarts, and Adaetum feeds apps only the secrets they need.
13.8A. External Secrets, Secrets Store CSI and Reloader: how secrets reach apps and keep them current.
13.8B. Rotate a secret in the vault, and the apps restart with the new one.
13.9A. Authentik: single sign-on, so one login opens every tool you run.
13.9B. Adaetum sets it up from files in Git, so sign-in is configured, not clicked together.
13.10A. Kubewarden and Kubescape: rules that stop risky changes, and scans that find weak spots.
13.10B. The rules are code in Git, and new ones start in monitor mode before they block.

Networking and edge
13.11A. Tailscale: a private network between your machines, laptops and tools.
13.11B. Nodes join with scoped, short-lived keys that Adaetum creates for you.
13.12A. ingress-nginx, external-dns and kube-vip: one front door, automatic DNS records, and a floating address.
13.12B. Add an app, and its address appears. If a machine fails, the address moves.
13.13A. Cloudflare Tunnel: public access without opening ports on your network.
13.13B. Adaetum builds the tunnel and the DNS records during setup.

Watching and sizing
13.14A. Prometheus, Grafana and Alertmanager: the standard way to see metrics and get alerts.
13.14B. Alerts reach your phone through ntfy. An assistant can read dashboards through a Grafana connection.
13.15A. Kured and the autoscalers: reboots one at a time, and apps that grow and shrink with load.
13.15B. Each tool owns one narrow job, and Git stays the only source of policy.

Adaetum's own parts
13.16A. A host agent and web app: one place to see health, apps, security and automation.
13.16B. Find apps searches public charts and images, and drafts a reviewed change. It installs nothing itself.
13.17A. The assistant: explains problems in plain language, with memory of past fixes.
13.17B. It's off until you switch it on, and it asks before it changes anything.
Closing. Every part is replaceable. What Adaetum adds is the wiring, the defaults, and the help.

## Wrap
69. Standard parts, less complexity, and you stay in control.

## Corrections to what is already built
- Shot 15 says "Plug in an installer". The setup tool makes an installer image on your computer, and you write that to a USB stick. Wording should be "boot the installer".
- The earlier Tailscale test clip says the node key lasts one day (docs/integrations.md). docs/diagrams/first-boot.md says a one-time key valid for one hour. The docs disagree, so confirm with the maintainers before the film states a number.
- The README says a read-only MCP server lets other AI inspect a host. The in-cluster MCP servers (pods/mcp) are full-access on purpose, gated by network policy, tokens and client approval. Copy must not call them read-only.

## Where AI appears (it runs through the whole film, not only Act 9)
- Shots 1, 3: AI tools are one of the things you want to host yourselves. The AI icon is already one of the five services.
- Shot 14: the first mention of an assistant that helps you operate the cluster.
- Act 8: the assistant drawer ("Fix with assistant" buttons on the Workloads cards).
- Act 9: the assistant asks before it changes anything; other AI tools connect through standard connections.
- Wrap: AI helps with setup and upkeep, and a person still approves.

## Backup status in the docs (read before writing any backup claim)
- TODO.md, "Backup and restore": not started. No Longhorn backup jobs, Velero, or etcd snapshot export is configured. The R2 bucket exists as a target.
- docs/comparison.md: "No off-site backups by default yet. Application data volumes are not backed up off-site automatically."
- What exists today: configuration as code in a private repository you own (and Gitea mirrors out to it), an encrypted recovery kit and a standalone OpenBao backup exported in Phase 99, and Longhorn replicas across nodes (that is availability, not backup).
- A restore drill has not been documented or proven yet.
So the film may say "planned" for off-site data backups, never "included".

## Visual language (set 2026-11-14): friendly machine-cult sanctuary
- Servers are shrines: glowing stone obelisks with brass bands, sigils down the front, and a halo that wakes with power.
- Services (storage, music, video, docs, AI) are relics on altars, each with a brass halo.
- People are hooded acolytes in cloaks, with softly glowing eyes and a gold emblem. Friendly, not sinister. The occult is normal here.
- Rooms are candlelit temples: stone pillars, braziers with flickering flames, brass sigil rings on the floor.
- A laptop is a tome with glowing pages. Tributes and bills are gold.
- Colour: blue is the arcane cluster glow, warm is a lone shrine, red is failure, green is health, gold is tribute.
- Inspiration: Warhammer 40k machine-god styling, softened. Keep it warm, never grim.

## Visual language: placeholder-removal pass (pass 5)
- Cables are chains of brass beads along the tubes; warnings are turning red seals.
- VMs are carved reliquary cells inside a brass-framed glass reliquary.
- The rented data center is a stepped ziggurat with lit windows and a flame on top; the fence is brass with finials.
- The balancer is a brass gyroscope with three turning rings and a glowing core; requests are glowing parchment petitions.
- Chapels are solid dioramas: three stone walls, a timber roof frame, a steeple with a gold finial, glowing windows.
- The globe is a painted orrery on a brass stand with turning armillary rings.
- The cost steps are three stone tiers with a coin stack and a tangled knot beside each, topped by a chapel, two chapels, and an orrery.
- The cluster stands on a round tiered dais. The dashboard sits in a brass and lapis frame between marble candle posts.

## Visual language: temple palette (pass 4)
- Colours: umber night, sandstone walls, ivory marble with dark checker tiles, crimson drapes and carpet, lapis blue bands, aged gold. Cloaks are lapis and saffron.
- Blue remains the cluster's sacred glow, and red/green remain failure/health.
- Rooms have a crimson carpet with a gold border, drapes by the pillars, marble pillars with lapis bands, and incense smoke rising from braziers.
- The globe is a bronze orrery with gold lines. Platforms are sandstone.

## Visual language additions (pass 3)
- Places are chapels: your altar, a friend's altar. Chapel outlines have a steeple topped with a gold finial, and glowing windows.
- Banners show a gold A for Adaetum.
- Tribute is real coins with an A on both faces, kept in offering chests. Altars carry the relics, with candles at the corners.
- Strikes are lightning and fire, not a dagger or bolt shape. A struck chapel or shrine burns.
- Drift is shown as accumulated patches: riveted plates, bolted-on modules, candle sconces, cables, and scrawled runes.
- The tower is ceremonial: five app blocks on an altar ringed with candles. When it falls, the candles go out.
- Kubernetes is an all-seeing, many-eyed wheel, with the five apps orbiting it.
- The assistant is an oracle you appoint: a crystal orb with a watching eye.

## Every shot stands alone (decided 2026-11-14)
- A shared world, one continuous camera and persistent props were tried and reversed. Each shot is turned into video by H3 separately, so nothing can be guaranteed to look the same between shots. A prop that "persists" would look different in each generated clip.
- So every shot is self-contained: its own room, its own props and its own camera path, with cuts between shots. Consistency comes from a shared visual language (palette, banners, acolytes, shrines, altars), not from shared objects.
- Each shot should read clearly on its own: one subject, close enough to see detail, with the hall or ground behind it.
- The experiments are archived in case/_unused (world.js, stage.js) and are not imported.
- The finale (shots 150 and 151) is still one continuous scene across two shots; it can be merged into a single shot, or made self-contained, if that is wanted.

## Rules for every shot
- No text in the 3D scene. Meaning comes from objects, motion and colour.
- Colours: blue = cluster/network, warm = single server, green = healthy, red = failure, gold = money.
- First and last half second are calm so narration can start and end cleanly.
- Shots 1-12 still show gold coins and a disc. Visuals to change: coins become bills or a draining meter, disc becomes a USB installer.

## Set redesign: the Adytum (Greco-Roman cult temple)
"Adaetum" is said A-DAY-tum, a play on adytum, the inner sanctum of a Greek temple. The shared props in `case/lib.js` now carry this:
- Room: raised stylobate with fluted doric columns, a triglyph frieze and gold greek-key cornice, ashlar walls, a framed inner doorway, leather hangings, gold-A banners, wall torches and bronze tripod braziers.
- Servers: arks (gilded chest, carrying poles, winged lid) on stone plinths with a greek-key band. Spell runes glow on the face and a rune halo floats above.
- Services: relics ringed by rotating spell-rune circles. Altars carry glowing runes.
- Acolytes: hooded cloaks with leather belts, leather shoulder mantles and gold studs. Palette: sandstone, travertine, leather browns, bronze and gold, with crimson accents.
Helpers: `keyMat` (greek key), `runeTexture`/`runeMat`/`runePlane` (procedural runes). Prior version backed up in the scratchpad as lib.before_adytum.js.

## One take: shots 1-5
Shots 1-5 are one continuous take cut into slices (`case/shots/act1.js`). A single timeline T drives the camera, coin stream, bows, relics, table, server and background marchers,
so every cut lands on the exact state the previous shot ended in. Each shot still builds its own copy of the set, so any slice also renders alone for H3.

## Continuity pass, shots 1-18
- Shots 1-5 are one take driven by a single timeline (see above).
- Shots 6-8 stay in the same hall with the same two friends (standing at the sides where shot 5 left them). Each shot starts on the previous shot's final camera pose and final state:
  the dead-server table becomes the laptop table, the laptop table sinks as the glass host rises, and the host dissolves as the rented plot arrives.
- Shots 9-18 use a larger version of the same hall (colonnade, carpet, doorway, torches) instead of a bare floor, and each camera starts where the previous one ended. The tower (shot 12) and Kubernetes (shot 13)
  also use the hall, with the friends at their usual spots.
- Still hard cuts (different subject on purpose): the globe (shot 17) and everything from shot 19 on.
