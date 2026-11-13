import { Button, Callout, Card, Code, Flex, Heading, IconButton, Skeleton, Text } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { Check, CircleAlert, Copy, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Node, Report } from "../api";
import { AppLocation } from "../app/routes";
import { useNow } from "../app/useReport";
import { useToken } from "../lib/auth";
import { Login } from "./Login";
import { DataTable } from "../components/DataTable";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { HostCards, hostCards } from "../components/HostCards";
import { ThisServer } from "../components/ThisServer";
import { ReleaseCard } from "../components/ReleaseCard";
import {
  HostSummary,
  ClusterView,
  InstallPlan,
  JoinToken,
  ServerState,
  UnmatchedCheckIn,
  approveHost,
  mintJoinToken,
  useCluster,
} from "../lib/clusterApi";
import { formatAgo, plural } from "../lib/format";
import { Tone } from "../lib/status";

export function Cluster({ report, location }: { report: Report | null; location: AppLocation }) {
  const { data, error, notClusterAgent, authRequired, loading, refresh } = useCluster();
  const token = useToken();
  const now = new Date(useNow(30_000));

  // An alert about this machine ("focus=server") scrolls to its section.
  const focus = location.params.get("focus");
  useEffect(() => {
    if (focus === "server") document.getElementById("this-server")?.scrollIntoView({ block: "start" });
  }, [focus, loading]);

  return (
    <>
      <PageHeader
        title="Cluster"
        description="Which servers are running the Adaetum agent, and which need attention."
        actions={
          <Button variant="soft" color="gray" onClick={refresh} loading={loading}>
            <RefreshCw size={14} aria-hidden /> Refresh
          </Button>
        }
      />
      {authRequired ? (
        <Login rejected={token !== ""} onSignedIn={refresh} />
      ) : notClusterAgent ? (
        <Callout.Root color="gray" role="status">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>
            This is a single server's own agent. The cluster view lives on the cluster agent, which collects reports from every
            server's agent. Open the cluster agent's address to see it.
          </Callout.Text>
        </Callout.Root>
      ) : error && !data ? (
        <Callout.Root color="red" role="alert">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>Couldn't read the cluster: {error}.</Callout.Text>
        </Callout.Root>
      ) : !data ? (
        <Flex direction="column" gap="4" aria-busy="true" aria-label="Loading">
          <Skeleton height="72px" />
          <Skeleton height="200px" />
        </Flex>
      ) : (
        <Content data={data} nodes={report?.rke2.nodes ?? []} now={now} onChanged={refresh} />
      )}
      {/* Only a server's own agent knows its machine; the cluster agent's report has no hostname. */}
      {report?.host.hostname && (
        <div id="this-server" style={{ marginTop: "var(--space-6)" }}>
          <Section title="This server">
            <Text size="2" color="gray">The machine this agent is reporting on.</Text>
            <ThisServer report={report} />
          </Section>
        </div>
      )}
    </>
  );
}

function Content({ data, nodes, now, onChanged }: { data: ClusterView; nodes: Node[]; now: Date; onChanged: () => void }) {
  return (
    <Flex direction="column" gap="6">
      <Summary data={data} />
      <Section title="Adaetum updates"><ReleaseCard servers={data.servers} nodesError={data.nodesError} /></Section>
      {data.pending.length > 0 && (
        <Section title="Waiting for approval">
          <PendingHosts hosts={data.pending} now={now} onChanged={onChanged} />
        </Section>
      )}
      {!data.nodesError && (
        <Section title="Servers in the cluster">
          <Text size="2" color="gray">Last backup: none yet. Backups aren't set up on this cluster.</Text>
          <HostCards cards={hostCards(data.servers, nodes)} now={now} />
        </Section>
      )}
      {data.install.actions.length > 0 && (
        <Section title="Fix it">
          <InstallSuggestion plan={data.install} />
        </Section>
      )}
      {data.unmatched.length > 0 && (
        <Section title="Agents on machines outside the cluster">
          <Text size="2" color="gray">
            These machines run the agent and have joined, but the cluster doesn't list them as servers. That's expected for a machine
            that is about to join, was removed, or is being managed from outside.
          </Text>
          <DataTable
            data={data.unmatched}
            columns={unmatchedColumns(now)}
            getRowId={(item) => item.hostId}
            noun="machines"
            pageSize={10}
          />
        </Section>
      )}
      <Section title="Add a machine">
        <AddMachine />
      </Section>
    </Flex>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Flex direction="column" gap="3" asChild>
      <section>
        <Heading size="4" as="h2">{title}</Heading>
        {children}
      </section>
    </Flex>
  );
}

// Summary answers the one question this page exists for, first.
function Summary({ data }: { data: ClusterView }) {
  if (data.nodesError) {
    return (
      <Callout.Root color="amber" highContrast role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>
          The cluster's server list couldn't be read ({data.nodesError}), so servers can't be checked against the agents yet.
          Joined machines are still listed below.
        </Callout.Text>
      </Callout.Root>
    );
  }
  const count = (state: ServerState) => data.servers.filter((server) => server.status === state).length;
  const missing = count("missing");
  const quiet = count("stale");
  const outdated = count("outdated");

  if (data.servers.length === 0) {
    return (
      <Callout.Root color="gray" role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>The cluster lists no servers yet.</Callout.Text>
      </Callout.Root>
    );
  }
  if (missing + quiet + outdated === 0) {
    return (
      <Callout.Root color="green" highContrast role="status">
        <Callout.Icon><Check size={16} /></Callout.Icon>
        <Callout.Text>All {plural(data.servers.length, "server")} are running an up-to-date agent.</Callout.Text>
      </Callout.Root>
    );
  }
  const parts = [
    missing > 0 && `${plural(missing, "server")} without the agent`,
    quiet > 0 && `${plural(quiet, "server")} not heard from lately`,
    outdated > 0 && `${plural(outdated, "server")} needing an update`,
  ].filter(Boolean);
  return (
    <Callout.Root color={missing > 0 ? "red" : "amber"} highContrast role="alert">
      <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
      <Callout.Text>
        {parts.join(", ")}. The agent is what lets you reach a server directly when the cluster is down, so these are worth fixing.
      </Callout.Text>
    </Callout.Root>
  );
}

// InstallSuggestion says how to put the agent on the servers that lack it or run an
// old one. Ansible over SSH already has administrator access to every server, so the
// same playbook installs, upgrades, and repairs. Running it from this page is not
// built yet, so this shows exactly what to run.
function InstallSuggestion({ plan }: { plan: InstallPlan }) {
  const installs = plan.actions.filter((action) => action.kind === "install").length;
  const upgrades = plan.actions.length - installs;
  const launcher = `#/automation?playbook=host-agent&limit=${encodeURIComponent(plan.limit)}`;
  const command = `ansible-playbook playbooks/host-agent.yml --limit ${plan.limit} -e host_agent_binary_src=/path/to/adaetum-agent --check`;
  return (
    <Card size="2">
      <Flex direction="column" gap="3">
        <Text size="2">
          {[installs > 0 && `Install the agent on ${plural(installs, "server")}`, upgrades > 0 && `update it on ${plural(upgrades, "server")}`]
            .filter(Boolean)
            .join(" and ")}
          . It goes over the same secure remote access the automation already uses, one server at a time.
        </Text>
        <Text size="2" color="gray">
          Open it in Automation, where the servers are already filled in. It starts as a preview, which shows what would
          change without changing anything.
        </Text>
        <div>
          <Button asChild>
            <a href={launcher}>Open in Automation</a>
          </Button>
        </div>
        <Text size="1" color="gray">Or run it yourself from a machine with Ansible and the cluster's SSH access:</Text>
        <Code size="2" style={{ display: "block", overflowWrap: "anywhere", padding: "var(--space-2)" }}>{command}</Code>
        {plan.skipped?.map((skipped) => (
          <Text size="1" color="gray" key={skipped.server}>
            Left alone: {skipped.server} — {skipped.reason}.
          </Text>
        ))}
      </Flex>
    </Card>
  );
}

function unmatchedColumns(now: Date): ColumnDef<UnmatchedCheckIn, any>[] {
  return [
    { id: "host", header: "Machine", accessorFn: (row) => row.hostname, cell: ({ row }) => <Code variant="ghost">{row.original.hostname}</Code> },
    { id: "seen", header: "Last heard from", accessorFn: (row) => row.at, cell: ({ row }) => <Text size="2">{formatAgo(row.original.at, now)}</Text> },
    { id: "version", header: "Agent version", accessorFn: (row) => row.version, cell: ({ row }) => <Text size="2">{row.original.version || "—"}</Text> },
  ];
}

function PendingHosts({ hosts, now, onChanged }: { hosts: HostSummary[]; now: Date; onChanged: () => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const approve = async (host: HostSummary) => {
    setBusy(host.hostId);
    setFailure(null);
    try {
      await approveHost(host.hostId);
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  return (
    <Flex direction="column" gap="3">
      <Text size="2" color="gray">
        These machines presented a valid join token but aren't servers in the cluster, so a person has to confirm them. Approve only
        machines you recognise.
      </Text>
      {failure && (
        <Callout.Root color="red" role="alert">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>{failure}</Callout.Text>
        </Callout.Root>
      )}
      {hosts.map((host) => (
        <Card key={host.hostId} size="2">
          <Flex justify="between" align="center" gap="3" wrap="wrap">
            <Flex direction="column" gap="1">
              <Text weight="medium">{host.hostname}</Text>
              <Text size="1" color="gray">Asked to join {formatAgo(host.enrolledAt, now)}. Machine ID {host.hostId.slice(0, 8)}…</Text>
            </Flex>
            <Button onClick={() => approve(host)} disabled={busy !== null}>
              {busy === host.hostId ? "Approving…" : "Approve"}
            </Button>
          </Flex>
        </Card>
      ))}
    </Flex>
  );
}

// AddMachine makes the one-time join token. The secret is shown once, here, and
// the cluster keeps only a hash of it.
function AddMachine() {
  const [token, setToken] = useState<JoinToken | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const make = async () => {
    setBusy(true);
    setFailure(null);
    setCopied(false);
    try {
      setToken(await mintJoinToken("added from the Cluster page", 60));
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!token) return;
    try {
      await navigator.clipboard.writeText(token.secret);
      setCopied(true);
    } catch {
      /* the token is visible, so it can still be copied by hand */
    }
  };

  return (
    <Card size="2">
      <Flex direction="column" gap="3">
        <Text size="2" color="gray">
          To bring another machine under management, make a one-time join token (good for an hour), put it in a file on that
          machine, and run <Code variant="ghost">adaetum-agent join</Code> there. A machine that is already a server in the cluster is
          approved automatically; any other waits for your approval above.
        </Text>
        {failure && (
          <Callout.Root color="red" role="alert">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{failure}</Callout.Text>
          </Callout.Root>
        )}
        {token ? (
          <Flex direction="column" gap="2">
            <Text size="2" weight="medium">Your join token. It is shown only now, so copy it before leaving this page.</Text>
            <Flex align="center" gap="2">
              {/* Never shared with the assistant: see lib/pageContext.ts. */}
              <Code size="2" style={{ overflowWrap: "anywhere" }} data-private>{token.secret}</Code>
              <IconButton variant="soft" color="gray" aria-label={copied ? "Copied" : "Copy token"} onClick={copy}>
                {copied ? <Check size={14} /> : <Copy size={14} />}
              </IconButton>
            </Flex>
            <Text size="1" color="gray">Expires {new Date(token.expiresAt).toLocaleString()} and works once.</Text>
          </Flex>
        ) : (
          <div>
            <Button variant="soft" onClick={make} disabled={busy}>{busy ? "Making…" : "Make a join token"}</Button>
          </div>
        )}
      </Flex>
    </Card>
  );
}
