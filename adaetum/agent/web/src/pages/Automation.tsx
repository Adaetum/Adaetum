import { Button, Callout, Card, Code, Flex, Heading, Skeleton, Text } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { CircleAlert, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { AppLocation } from "../app/routes";
import { useNow } from "../app/useReport";
import { Advanced, AdvancedBlock } from "../components/Advanced";
import { LaunchCard } from "../components/ansible/LaunchCard";
import { RunPanel } from "../components/ansible/RunPanel";
import { Run as LaunchedRun, listRuns, useCapabilities } from "../lib/ansibleApi";
import { DataTable } from "../components/DataTable";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import {
  AutomationOverview,
  Declared,
  Playbook,
  Run,
  RunStatus,
  Runner,
  useAutomation,
} from "../lib/automationApi";
import { formatAgo, plural } from "../lib/format";
import { Tone } from "../lib/status";

const RUN_STATUS: Record<RunStatus, { label: string; tone: Tone }> = {
  success: { label: "Succeeded", tone: "green" },
  failed: { label: "A step failed", tone: "red" },
  unreachable: { label: "Server unreachable", tone: "red" },
  incomplete: { label: "Unfinished", tone: "amber" },
};

export function Automation({ location }: { location: AppLocation }) {
  const { data, error, loading, refresh } = useAutomation();
  const now = new Date(useNow(30_000));

  return (
    <>
      <PageHeader
        title="Automation"
        description="The routine jobs that keep your servers in shape, and one-off tasks you can start yourself."
        actions={
          <Button variant="soft" color="gray" onClick={refresh} disabled={loading}>
            <RefreshCw size={14} aria-hidden /> Refresh
          </Button>
        }
      />
      {error && !data ? (
        <Callout.Root color="red" role="alert">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>Couldn't read the automation state: {error}.</Callout.Text>
        </Callout.Root>
      ) : !data ? (
        <Flex direction="column" gap="4" aria-busy="true" aria-label="Loading">
          <Skeleton height="96px" />
          <Skeleton height="200px" />
        </Flex>
      ) : (
        <Content data={data} now={now} location={location} />
      )}
    </>
  );
}

function Content({ data, now, location }: { data: AutomationOverview; now: Date; location: AppLocation }) {
  return (
    <Flex direction="column" gap="6">
      <HealthBanner data={data} now={now} />
      {data.errors && data.errors.length > 0 && (
        <Callout.Root color="amber" highContrast role="status">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>Some details couldn't be read: {data.errors.join("; ")}.</Callout.Text>
        </Callout.Root>
      )}
      {GROUPS.map((group) => (
        <Section key={group.title} title={group.title} note={group.note}>
          <div className="tile-grid">
            {group.items.map((item) => <AutomationCard key={item.title} item={item} data={data} />)}
          </div>
        </Section>
      ))}
      <Launcher location={location} now={now} />
      <Advanced>
        <AdvancedBlock title="History" note="Each time the automation ran on its own timer, newest first.">
          <DataTable
            data={data.runs}
            columns={runColumns(now)}
            getRowId={(run) => `${run.startedAt}|${run.playbook}`}
            noun="runs"
            pageSize={10}
            empty="Nothing has run yet."
          />
        </AdvancedBlock>
        <AdvancedBlock title="Where the settings live" note="Settings are changed in platform.yaml in your recovery repository, then applied on the next run. This page only shows them.">
          <table className="sizing-table">
            <thead><tr><th>Automation</th><th>Setting in platform.yaml</th></tr></thead>
            <tbody>
              <tr><td>Updates and restarts</td><td><Code variant="ghost">spec.hostMaintenance</Code></td></tr>
              <tr><td>Free up disk space</td><td><Code variant="ghost">spec.imageCleanup</Code></td></tr>
              <tr><td>Share downloads between servers</td><td><Code variant="ghost">spec.imageSharing</Code></td></tr>
            </tbody>
          </table>
        </AdvancedBlock>
        <AdvancedBlock title="Every task available" note="These are Ansible playbooks: the files the automation runs. The ones in “Run a task” can be started from this page.">
          <DataTable
            data={data.playbooks}
            columns={playbookColumns}
            getRowId={(playbook) => playbook.file}
            noun="tasks"
            pageSize={10}
            empty="No tasks found."
          />
        </AdvancedBlock>
        <RunnerDetails runner={data.runner} />
      </Advanced>
    </Flex>
  );
}

// What the automation does, in plain words. Each card is one thing a person would otherwise
// have to keep doing by hand on every server. `state` and `lines` come from the setting the
// platform asks for (platform.yaml); a card with a `task` can also be started from the
// "Run a task" section below.
interface Automation {
  title: string;
  what: string;
  task?: string;
  taskLabel?: string;
  setting?: (declared?: Declared) => { state: string; tone: Tone; lines: string[] };
  // True when the card is on because the automation itself is installed, not because of a setting.
  followsRunner?: boolean;
  // True for what the automation enforces on its own timer: if those runs are failing, the card
  // must not say "On", because nothing is actually being enforced.
  onTimer?: boolean;
}

const NOT_SET = ["Not turned on yet. The setting for it is listed under Advanced."];

const GROUPS: { title: string; note?: string; items: Automation[] }[] = [
  {
    title: "Runs on its own",
    note: "These are checked and put right on a timer, so servers don't drift from how they should be.",
    items: [
      {
        title: "Health checks",
        what: "Looks over every server (disk, memory, clock, network) and records the result.",
        followsRunner: true,
        onTimer: true,
      },
      {
        title: "Updates and restarts",
        what: "Keeps each server's software up to date, and restarts them one at a time in a quiet window.",
        onTimer: true,
        setting: (declared) => {
          const maintenance = declared?.hostMaintenance;
          return {
            state: maintenance ? (maintenance.enabled ? "On" : "Off") : "Not set",
            tone: maintenance?.enabled ? "green" : "gray",
            lines: maintenance ? [`Updates: ${maintenance.updatePolicy}`, `Restart window: ${maintenance.rebootWindow}`] : NOT_SET,
          };
        },
      },
      {
        title: "Administrator accounts",
        what: "Gives each server an administrator account for everyone in your Tailscale admin group, and removes it when they leave.",
        followsRunner: true,
        onTimer: true,
      },
    ],
  },
  {
    title: "Set up on each server",
    note: "These are installed once on every server and then run there by themselves.",
    items: [
      {
        title: "Free up disk space",
        what: "Removes container images nothing uses, so disks don't fill up.",
        task: "image-cleanup",
        taskLabel: "Set it up on servers…",
        setting: (declared) => {
          const cleanup = declared?.imageCleanup;
          return {
            state: cleanup ? (cleanup.enabled ? "On" : "Off") : "Not set",
            tone: cleanup?.enabled ? "green" : "gray",
            lines: cleanup
              ? [
                  `${cleanup.level === "aggressive" ? "Thorough" : "Careful"} cleanup of unused container images`,
                  `Every day at ${cleanup.time}`,
                  `Removes up to ${cleanup.maxImagesPerRun} images per run`,
                  cleanup.checkOnly ? "Check only: reports but does not remove" : "Removes images",
                ]
              : NOT_SET,
          };
        },
      },
      {
        title: "Share downloads between servers",
        what: "Servers fetch apps from each other before the internet, which is faster and saves bandwidth.",
        setting: (declared) => {
          const sharing = declared?.imageSharing;
          return {
            state: sharing ? (sharing.enabled ? "On" : "Off") : "Not set",
            tone: sharing?.enabled ? "green" : "gray",
            lines: sharing ? [`Registries: ${sharing.registries.join(", ") || "none"}`] : NOT_SET,
          };
        },
      },
    ],
  },
  {
    title: "Start it yourself",
    items: [
      {
        title: "Install or update the Adaetum agent",
        what: "Puts the Adaetum agent on a server, or brings an old one up to date. Safe to repeat.",
        task: "host-agent",
        taskLabel: "Choose servers…",
      },
    ],
  },
];

function AutomationCard({ item, data }: { item: Automation; data: AutomationOverview }) {
  const setting = item.setting?.(data.declared);
  let state = setting ? { label: setting.state, tone: setting.tone } : item.followsRunner ? { label: data.runner.found ? "On" : "Not installed", tone: (data.runner.found ? "green" : "gray") as Tone } : null;
  if (item.onTimer && state?.label === "On" && data.health.consecutiveFailures > 0) state = { label: "Not working", tone: "red" };
  return (
    <Card size="2">
      <Flex direction="column" gap="2" height="100%">
        <Flex justify="between" align="center" gap="2">
          <Heading size="3" as="h3">{item.title}</Heading>
          {state && <StatusBadge tone={state.tone}>{state.label}</StatusBadge>}
        </Flex>
        <Text size="2">{item.what}</Text>
        {setting?.lines.map((line) => <Text key={line} size="2" color="gray">{line}</Text>)}
        {item.task && (
          <div style={{ marginTop: "auto", paddingTop: "var(--space-2)" }}>
            <Button size="1" variant="soft" onClick={() => openTask(item.task!)}>{item.taskLabel ?? "Run it…"}</Button>
          </div>
        )}
      </Flex>
    </Card>
  );
}

// Pre-selects a task in the launcher and brings it into view.
function openTask(id: string) {
  window.location.hash = `#/automation?playbook=${encodeURIComponent(id)}`;
  window.setTimeout(() => document.getElementById("run-a-task")?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
}

// Launcher is where a person runs a playbook on demand. The agent only actuates
// Ansible: it runs what the catalog lists, with the settings filled in here.
function Launcher({ location, now }: { location: AppLocation; now: Date }) {
  const { data: capabilities, error } = useCapabilities();
  const [runId, setRunId] = useState<string | null>(null);

  return (
    <Section title="Run a task" note="Start any of the tasks above on the servers you choose. Preview first: it shows what would change and changes nothing.">
      {error ? (
        <Text size="2" color="gray">Couldn't read what this agent can run: {error}.</Text>
      ) : !capabilities ? (
        <Skeleton height="120px" />
      ) : (
        <>
          <LaunchCard
            key={location.params.toString()}
            capabilities={capabilities}
            initialPlaybook={location.params.get("playbook") ?? undefined}
            initialLimit={location.params.get("limit") ?? undefined}
            initialValues={Object.fromEntries(["host_agent_binary_url", "host_agent_binary_checksum", "host_agent_auth_enabled", "host_agent_auth_binary_url", "host_agent_auth_binary_checksum"]
              .flatMap((name) => { const value = location.params.get(name); return value ? [[name, value]] : []; }))}
            onStarted={setRunId}
          />
          {runId && <RunPanel id={runId} onClose={() => setRunId(null)} />}
          {capabilities.enabled && <RecentLaunches current={runId} now={now} onOpen={setRunId} />}
        </>
      )}
    </Section>
  );
}

function RecentLaunches({ current, now, onOpen }: { current: string | null; now: Date; onOpen: (id: string) => void }) {
  const [runs, setRuns] = useState<LaunchedRun[]>([]);
  // Reload when a run starts or the open one changes; finished runs appear after.
  useEffect(() => {
    const controller = new AbortController();
    listRuns(controller.signal).then(setRuns).catch(() => undefined);
    const timer = setInterval(() => listRuns(controller.signal).then(setRuns).catch(() => undefined), 5000);
    return () => { controller.abort(); clearInterval(timer); };
  }, [current]);

  if (runs.length === 0) return null;
  return (
    <Flex direction="column" gap="2">
      <Heading size="3" as="h3">Tasks started from here</Heading>
      {runs.slice(0, 5).map((run) => (
        <Button key={run.id} variant="soft" color="gray" onClick={() => onOpen(run.id)} style={{ justifyContent: "flex-start" }}>
          {run.title} · {run.limit.join(", ")} · {run.check ? "preview" : "real run"} · {run.status} · {formatAgo(run.startedAt, now)}
        </Button>
      ))}
    </Flex>
  );
}

function Section({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
  return (
    <Flex direction="column" gap="3" asChild>
      <section id={title === "Run a task" ? "run-a-task" : undefined}>
        <Heading size="4" as="h2">{title}</Heading>
        {note && <Text size="2" color="gray">{note}</Text>}
        {children}
      </section>
    </Flex>
  );
}

function HealthBanner({ data, now }: { data: AutomationOverview; now: Date }) {
  const { runner, health } = data;
  if (!runner.found) {
    return (
      <Callout.Root color="gray" role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>Automation isn't set up on this cluster yet.</Callout.Text>
      </Callout.Root>
    );
  }
  if (health.total === 0) {
    return (
      <Callout.Root color="gray" role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>Automation is installed but hasn't run yet.</Callout.Text>
      </Callout.Root>
    );
  }
  if (health.consecutiveFailures > 0) {
    return (
      <Callout.Root color="red" highContrast role="alert">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>
          The last {plural(health.consecutiveFailures, "automatic run")} did not succeed
          {/* With no success on record, the streak may be older than the history we can see. */}
          {health.failingSince && <>, since {health.lastSuccess ? "" : "at least "}{formatAgo(health.failingSince, now)}</>}.
          {health.lastError && <> Latest problem: <Code variant="ghost">{health.lastError}</Code></>}
          {health.lastSuccess ? <> Last success was {formatAgo(health.lastSuccess, now)}.</> : <> It has not succeeded in the recorded history.</>}
        </Callout.Text>
      </Callout.Root>
    );
  }
  return (
    <Callout.Root color="green" highContrast role="status">
      <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
      <Callout.Text>
        Automation is healthy: {health.succeeded} of the last {plural(health.total, "automatic run")} succeeded
        {health.lastSuccess && <>, most recently {formatAgo(health.lastSuccess, now)}</>}.
      </Callout.Text>
    </Callout.Root>
  );
}

function serverNames(run: Run): string[] {
  return run.hosts.map((host) => host.host).filter((host) => host !== "localhost");
}

function runColumns(now: Date): ColumnDef<Run, any>[] {
  return [
    {
      id: "when",
      header: "Started",
      accessorFn: (run) => run.startedAt,
      cell: ({ row }) => (
        <Text size="2" title={row.original.startedAt}>{formatAgo(row.original.startedAt, now)}</Text>
      ),
    },
    { id: "playbook", header: "Task", accessorFn: (run) => run.playbook, cell: ({ row }) => <Code variant="ghost">{row.original.playbook}</Code> },
    {
      id: "status",
      header: "Result",
      accessorFn: (run) => RUN_STATUS[run.status].label,
      cell: ({ row }) => (
        <StatusBadge tone={RUN_STATUS[row.original.status].tone}>{RUN_STATUS[row.original.status].label}</StatusBadge>
      ),
    },
    {
      id: "servers",
      header: "Servers",
      // "localhost" is the runner's own bookkeeping play, not a server to act on.
      accessorFn: (run) => serverNames(run).join(" "),
      cell: ({ row }) => <Text size="2" className="mono" wrap="nowrap">{serverNames(row.original).join(", ") || "—"}</Text>,
      enableSorting: false,
    },
    { id: "changes", header: "Changes", accessorFn: (run) => run.changed, cell: ({ row }) => <Text size="2">{row.original.changed}</Text> },
    {
      id: "problem",
      header: "What went wrong",
      accessorFn: (run) => run.error ?? "",
      cell: ({ row }) => (
        <Text size="2" color="gray" as="div" className="clamp" style={{ minWidth: "18rem", maxWidth: "30rem", overflowWrap: "anywhere" }}>
          {row.original.error ?? "—"}
        </Text>
      ),
      enableSorting: false,
    },
  ];
}

const playbookColumns: ColumnDef<Playbook, any>[] = [
  { id: "file", header: "Task file", accessorFn: (playbook) => playbook.file, cell: ({ row }) => <Code variant="ghost">{row.original.file}</Code> },
  {
    id: "description",
    header: "What it does",
    accessorFn: (playbook) => playbook.description ?? "",
    cell: ({ row }) => (
      <Text size="2" color="gray" as="div" className="clamp" style={{ minWidth: "18rem", maxWidth: "40rem", overflowWrap: "anywhere" }}>
        {row.original.description || "No description."}
      </Text>
    ),
    enableSorting: false,
  },
];

function RunnerDetails({ runner }: { runner: Runner }) {
  if (!runner.found) return null;
  const rows: [string, string][] = [
    ["Runs", runner.schedule ? `On a timer (cron ${runner.schedule})` : "—"],
    ["Playbook", runner.playbook ?? "—"],
    ["Where", runner.pod ? `${runner.pod.name} on ${runner.pod.node} (${runner.pod.phase.toLowerCase()})` : "No running copy"],
    ["Version", runner.image ?? "—"],
  ];
  return (
    <AdvancedBlock title="The automation worker" note="The program on the cluster that runs the jobs above on a timer.">
      <Card size="2">
        <dl className="detail-list">
          {rows.map(([label, value]) => (
            <div key={label}>
              <dt><Text size="2" color="gray">{label}</Text></dt>
              <dd><Text size="2" className="mono">{value}</Text></dd>
            </div>
          ))}
        </dl>
      </Card>
    </AdvancedBlock>
  );
}
