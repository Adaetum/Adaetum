import { Badge, Button, Callout, Flex, Heading, Text } from "@radix-ui/themes";
import { ExternalLink, Info, MessageCircle } from "lucide-react";
import { ArgoApp } from "../api";
import { AppCard } from "../lib/apps";
import { formatBytes } from "../lib/format";
import { useSeries } from "../lib/historyApi";
import { Amounts, Sizing, WorkloadSizing, formatCpu, formatMemory, toneOf } from "../lib/sizing";
import { VolumeSizing } from "../lib/storageSizing";
import { argoConverged } from "../lib/status";
import { componentKind, healthState, instanceState, syncState } from "../lib/vocabulary";
import { Radar } from "./Scorecard";
import { StatusBadge } from "./StatusBadge";
import { UsageChart } from "./UsageChart";
import { useAssistant } from "./assistant/AssistantProvider";

export type Series = ReturnType<typeof useSeries>;

const cpu = (value?: number) => (value ? formatCpu(value) : "not set");
const memory = (value?: number) => (value ? formatMemory(value) : "not set");

// One line per compute change worth making, "now → suggested".
function changes(workload: WorkloadSizing): string[] {
  const { request, limit, suggestion } = workload;
  const lines: string[] = [];
  const add = (label: string, now: string, want?: number, show?: (value?: number) => string) => {
    if (want) lines.push(`${label}: ${now} → ${show!(want)}`);
  };
  add("Memory request", memory(request.memoryBytes), suggestion.request.memoryBytes, memory);
  add("Memory limit", memory(limit.memoryBytes), suggestion.limit.memoryBytes, memory);
  add("CPU request", cpu(request.cpuMillis), suggestion.request.cpuMillis, cpu);
  add("CPU limit", cpu(limit.cpuMillis), suggestion.limit.cpuMillis, cpu);
  return lines;
}

// What the usage figures are based on, in a few words.
function basisText(sizing: Sizing): string {
  const recorded = sizing.workloads.filter((workload) => workload.basis.kind === "history");
  if (sizing.workloads.length === 0) return "";
  if (recorded.length === 0) return "Judged on one reading of right now: no usage history is recorded yet.";
  const hours = Math.min(...recorded.map((workload) => workload.basis.hours));
  const days = hours >= 48 ? `${(hours / 24).toFixed(1)} days` : `${hours} ${hours === 1 ? "hour" : "hours"}`;
  return hours >= 48 ? `Judged on ${days} of recorded use.` : `Judged on ${days} of recorded use so far (about two days are needed before values are suggested for shrinking).`;
}

const volumeLine = (volume: VolumeSizing) => `${volume.name}: ${formatBytes(volume.usedBytes ?? 0)} of ${formatBytes(volume.capacityBytes)} used`;

// What to ask the assistant to do about one volume. Growing happens in place; shrinking
// never does, so it is a migration, and the old volume is not removed until a person says so.
export function volumeFixAsk(card: AppCard, volume: VolumeSizing): string {
  const fix = volume.suggestion;
  if (!fix) return "";
  const head = `The dashboard says the storage volume ${volume.name} for ${card.name} is the wrong size (${volumeLine(volume)}).`;
  const findings = volume.reasons.map((reason) => `- ${reason}`).join("\n");
  const task =
    fix.kind === "grow"
      ? `Please make it ${formatBytes(fix.newBytes)}. ${volume.expandable === false ? "Its storage class does NOT allow expansion in place, so tell me what the options are." : "Its storage class allows expansion, so this is a change to the claim's storage request in pods/ that Argo CD applies and Longhorn expands in place. Volumes can never be shrunk afterwards, so confirm the size with me."} Find where it is defined in pods/, show me the exact change, and rehearse it before anything is applied.`
      : `It is much bigger than it needs. A volume cannot be shrunk in place, so please plan a migration to a ${formatBytes(fix.newBytes)} volume: create the new volume, copy the data, switch the app over, and only remove the old volume after I confirm it works. Show me the plan and rehearse it before anything is applied.`;
  return [head, findings && `What it found:\n${findings}`, task].filter(Boolean).join("\n\n");
}

// The question the Fix button hands to the assistant: everything found for this app, the
// numbers, and where the change has to go. The assistant never edits the cluster; the fix
// lands in the app's definition under pods/ and Argo CD applies it, like every other change.
export function fixAsk(card: AppCard, sizing: Sizing): string {
  const parts = sizing.workloads
    .map((workload) => {
      const wanted = changes(workload);
      return wanted.length ? `- ${workload.name} (${workload.copies} ${workload.copies === 1 ? "copy" : "copies"}): ${wanted.join("; ")}` : "";
    })
    .filter(Boolean);
  const volumes = sizing.storage.volumes.filter((volume) => volume.suggestion).map((volume) => `- ${volumeLine(volume)} → ${volume.suggestion!.kind === "grow" ? "grow" : "shrink (needs a migration)"} to ${formatBytes(volume.suggestion!.newBytes)}`);
  const problems = sizing.sections.flatMap((section) => section.reasons.slice(0, 2).map((reason) => `- ${section.label}: ${reason}`));
  return [
    `The dashboard scores the setup of ${card.name} at ${sizing.points}/100 (${sizing.grade}). Can you help me fix it?`,
    problems.length ? `What it found:\n${problems.join("\n")}` : "",
    parts.length ? `Suggested resource settings per copy (${basisText(sizing).replace(/\.$/, "")}; please sanity-check them before applying):\n${parts.join("\n")}` : "",
    volumes.length ? `Suggested storage changes (volumes can only be grown in place, never shrunk):\n${volumes.join("\n")}` : "",
    `Please find where ${card.name} is defined in pods/ and show me the exact changes, then rehearse them before anything is applied.`,
  ]
    .filter(Boolean)
    .join("\n\n");
}

const amounts = (value: Amounts) => `${cpu(value.cpuMillis)} CPU · ${memory(value.memoryBytes)}`;

// ---- Panels ------------------------------------------------------------------
//
// The same sections are used by the quick dialog (Apps page) and by the app's own page
// (Workloads), so both always say the same thing.

export function ScorePanel({ sizing }: { sizing: Sizing }) {
  return (
    <Flex direction="column" gap="4">
      <Flex gap="4" align="center" wrap="wrap">
        <Radar sizing={sizing} size={132} />
        <Flex direction="column" gap="1" style={{ flex: 1, minWidth: 200 }}>
          <Heading size="6" color={sizing.tone === "gray" ? undefined : sizing.tone}>{sizing.points}/100 · {sizing.grade}{sizing.acceptedPoints > 0 && <Text size="2" weight="regular" color="gray"> · {sizing.acceptedPoints} points accepted</Text>}</Heading>
          <Text size="2" color="gray">How well this app is set up: four scores, one for each way it can be wrong.</Text>
          <Text size="1" color="gray">{basisText(sizing)}</Text>
        </Flex>
      </Flex>

      {sizing.workloads.length > 0 && !sizing.measured && (
        <Callout.Root color="amber" size="1"><Callout.Icon><Info size={14} /></Callout.Icon><Callout.Text>No usage numbers are available, so Fit can't be judged.</Callout.Text></Callout.Root>
      )}

      <div className="app-detail-sections">
        {sizing.sections.map((section) => (
          <Flex key={section.key} direction="column" gap="1">
            <Flex justify="between" align="baseline">
              <Text size="2" weight="bold">{section.label}</Text>
              <Text size="2" weight="bold" color={section.points === null ? undefined : toneOf(section.points + section.accepted)}>{section.points === null ? "n/a" : `${section.points}/100`}</Text>
            </Flex>
            <Text size="1" color="gray">{section.asks}</Text>
            {section.points === null ? (
              <Text size="1" color="gray">
                {section.key === "storage" ? "It stores nothing on a volume." : section.key === "scaling" ? "Does not apply: there is no autoscaler, and this app is run as a fixed number of copies." : "Nothing is running to judge."}
              </Text>
            ) : section.reasons.length === 0 ? (
              <Text size="1">Nothing to fix.</Text>
            ) : (
              <ul style={{ margin: 0, paddingLeft: "1.1rem" }}>
                {section.reasons.map((reason) => <li key={reason}><Text size="1">{reason}</Text></li>)}
              </ul>
            )}
            {section.acceptedReasons.length > 0 && (
              // Accepted on purpose (the workload's adaetum.io/accepted annotation): counted in the
              // score, but not a problem to chase.
              <ul style={{ margin: 0, paddingLeft: "1.1rem" }}>
                {section.acceptedReasons.map((reason) => <li key={reason}><Text size="1" color="gray">Accepted: {reason}</Text></li>)}
              </ul>
            )}
          </Flex>
        ))}
      </div>
    </Flex>
  );
}

// Each volume: how full, how fast it is filling, and what to do about it. `linkFor`
// adds a "show in Longhorn" link when the cluster has one.
export function StoragePanel({ card, sizing, series, linkFor }: { card: AppCard; sizing: Sizing; series: Series; linkFor?: (volumeName: string) => string | undefined }) {
  const assistant = useAssistant();
  if (card.volumes.length === 0) return <Text size="2" color="gray">This app stores nothing on a volume.</Text>;
  return (
    <Flex direction="column" gap="4">
      {sizing.storage.volumes.map((volume) => {
        const recorded = series?.volumes.find((item) => item.volume === volume.name);
        const fullness = volume.usedBytes !== undefined && volume.capacityBytes ? volume.usedBytes / volume.capacityBytes : null;
        const link = linkFor?.(volume.name);
        return (
          <Flex key={volume.name} direction="column" gap="1">
            <Flex justify="between" align="baseline" gap="2">
              <Text size="2" weight="medium">{volume.name}</Text>
              <Text size="1" weight="bold" color={volume.points === null ? "gray" : toneOf(volume.points)}>{volume.points === null ? "not measured" : `${volume.points}/100`}</Text>
            </Flex>
            {fullness !== null && (
              <div className="util-track" role="meter" aria-label={`${volume.name} is ${Math.round(fullness * 100)}% full`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(fullness * 100)}>
                <div className={`util-fill util-${fullness >= 0.85 ? "red" : fullness >= 0.65 ? "amber" : "green"}`} style={{ width: `${Math.min(100, fullness * 100)}%` }} />
              </div>
            )}
            <Text size="1" color="gray">
              {volume.usedBytes !== undefined ? volumeLine(volume) : `${formatBytes(volume.capacityBytes)}, usage isn't measured for this kind of storage`}
              {volume.growthPerDay !== undefined ? ` · ${volume.growthPerDay > 0 ? `growing ${formatBytes(volume.growthPerDay)} a day` : "not growing"}` : ""}
              {volume.expandable === false ? " · can't be made bigger" : ""}
            </Text>
            {volume.reasons.length > 0 && (
              <ul style={{ margin: 0, paddingLeft: "1.1rem" }}>{volume.reasons.map((reason) => <li key={reason}><Text size="1">{reason}</Text></li>)}</ul>
            )}
            {recorded && recorded.points.length > 1 && (
              <UsageChart title="Filled over the last days" points={recorded.points.map((point) => ({ hour: point.hour, memoryPeak: point.usedBytes, memoryAvg: point.usedBytes, cpuPeak: 0, cpuAvg: 0 }))}
                pick={(point) => ({ peak: point.memoryPeak, avg: point.memoryAvg })} limit={volume.capacityBytes} limitLabel="size" format={formatBytes} />
            )}
            <Flex gap="2" align="center" wrap="wrap">
              {volume.suggestion && (
                <>
                  <Badge color={volume.suggestion.kind === "grow" ? "red" : "amber"} variant="soft">
                    {volume.suggestion.kind === "grow" ? `Grow to ${formatBytes(volume.suggestion.newBytes)}` : `Could be ${formatBytes(volume.suggestion.newBytes)}`}
                  </Badge>
                  <Text size="1" color="gray">{volume.suggestion.why}</Text>
                  <Button size="1" variant="soft" onClick={() => assistant.ask(volumeFixAsk(card, volume))}>
                    <MessageCircle size={12} aria-hidden /> Fix this volume
                  </Button>
                </>
              )}
              {link && <Button asChild size="1" variant="ghost" color="gray"><a href={link} target="_blank" rel="noreferrer noopener"><ExternalLink size={12} aria-hidden /> Show in Longhorn</a></Button>}
            </Flex>
          </Flex>
        );
      })}
    </Flex>
  );
}

// Use over the last days, what each part has and uses, and the settings worth changing.
export function ResourcesPanel({ sizing, series }: { sizing: Sizing; series: Series }) {
  if (sizing.workloads.length === 0) return <Text size="2" color="gray">Nothing is running, so there is nothing to size.</Text>;
  return (
    <Flex direction="column" gap="4">
      {series && series.workloads.length > 0 && (
        <Flex direction="column" gap="3">
          <Text size="2" weight="bold">Use over the last days</Text>
          {sizing.workloads.map((workload) => {
            const recorded = series.workloads.find((item) => item.workload === workload.name);
            return recorded ? (
              <Flex key={workload.name} direction="column" gap="1">
                {sizing.workloads.length > 1 && <Text size="1" color="gray">{workload.name}</Text>}
                <Flex gap="3" wrap="wrap">
                  <UsageChart title="Memory" points={recorded.points} pick={(point) => ({ peak: point.memoryPeak, avg: point.memoryAvg })}
                    request={workload.request.memoryBytes} limit={workload.limit.memoryBytes} format={formatMemory} />
                  <UsageChart title="CPU" points={recorded.points} pick={(point) => ({ peak: point.cpuPeak, avg: point.cpuAvg })}
                    request={workload.request.cpuMillis} limit={workload.limit.cpuMillis} format={formatCpu} />
                </Flex>
              </Flex>
            ) : null;
          })}
        </Flex>
      )}

      <Flex direction="column" gap="2">
        <Text size="2" weight="bold">What it has and uses, per copy</Text>
        <table className="sizing-table">
          <thead><tr><th>Part</th><th>Copies</th><th>Uses (highest)</th><th>Reserves</th><th>Allowed</th></tr></thead>
          <tbody>
            {sizing.workloads.map((workload) => (
              <tr key={workload.name}>
                <td>{workload.name}</td>
                <td>{workload.maxCopies > workload.copies ? `${workload.copies} (up to ${workload.maxCopies})` : workload.copies}</td>
                <td>{workload.usage ? amounts(workload.usage) : "—"}</td>
                <td>{amounts(workload.request)}</td>
                <td>{amounts(workload.limit)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Flex>

      {sizing.workloads.some((workload) => changes(workload).length > 0 || workload.suggestion.note.length > 0) && (
        <Flex direction="column" gap="2">
          <Flex gap="2" align="center"><Text size="2" weight="bold">Suggested settings</Text><Badge color={sizing.workloads.every((workload) => workload.basis.trusted) ? "green" : "amber"} variant="soft">{sizing.workloads.every((workload) => workload.basis.trusted) ? "from recorded use" : "early figures"}</Badge></Flex>
          {sizing.workloads.map((workload) => {
            const wanted = changes(workload);
            return wanted.length ? (
              <Flex key={workload.name} direction="column">
                <Text size="1" weight="medium">{workload.name}</Text>
                <ul style={{ margin: 0, paddingLeft: "1.1rem" }}>{wanted.map((line) => <li key={line}><Text size="1">{line}</Text></li>)}</ul>
              </Flex>
            ) : null;
          })}
          {[...new Set(sizing.workloads.flatMap((workload) => workload.suggestion.note))].map((note) => (
            <Text key={note} size="1" color="gray">{note}</Text>
          ))}
        </Flex>
      )}
    </Flex>
  );
}

// The deployments (Argo CD applications) that keep the app running.
export function DeliveryPanel({ card, linkFor }: { card: AppCard; linkFor?: (deployment: ArgoApp) => string | undefined }) {
  if (card.deployments.length === 0) return <Text size="2" color="gray">No deployment in the repository is recorded for this app.</Text>;
  return (
    <Flex direction="column" gap="2">
      <Text size="1" color="gray">The setup in the repository that keeps this app running. Anything out of date is waiting to be applied.</Text>
      {card.deployments.map((deployment) => {
        const sync = syncState(deployment.sync);
        const health = healthState(deployment.health);
        const link = linkFor?.(deployment);
        return (
          <Flex key={deployment.name} direction="column" gap="1">
            <Flex gap="2" align="center" wrap="wrap">
              <Text size="2" weight="medium">{deployment.name}</Text>
              <StatusBadge tone={sync.tone} title={deployment.sync}>{sync.label}</StatusBadge>
              <StatusBadge tone={health.tone} title={deployment.health}>{health.label}</StatusBadge>
              {deployment.revision && <Text size="1" color="gray">version {deployment.revision}</Text>}
              {link && <a href={link} target="_blank" rel="noreferrer noopener"><Text size="1">Show in Argo CD</Text></a>}
            </Flex>
            {deployment.chart && <Text size="1" color="gray">Made from the {deployment.chart} chart{deployment.chartRepo ? ` (${deployment.chartRepo})` : ""}.</Text>}
            {!argoConverged(deployment) && deployment.message && <Text size="1" color="red">{deployment.message}</Text>}
          </Flex>
        );
      })}
    </Flex>
  );
}

// The pieces this app is made of, for when something needs digging into.
export function PiecesPanel({ card }: { card: AppCard }) {
  if (card.components.length === 0 && card.instances.length === 0) return <Text size="2" color="gray">Nothing is running.</Text>;
  return (
    <Flex direction="column" gap="2">
      <table className="sizing-table">
        <thead><tr><th>Piece</th><th>Kind</th><th>Running</th></tr></thead>
        <tbody>
          {card.components.map((component) => (
            <tr key={`${component.kind}/${component.name}`}><td>{component.name}</td><td>{componentKind(component.kind)}</td><td>{component.ready} of {component.desired}</td></tr>
          ))}
        </tbody>
      </table>
      <table className="sizing-table">
        <thead><tr><th>Copy</th><th>Server</th><th>State</th><th>Restarts</th></tr></thead>
        <tbody>
          {card.instances.map((pod) => {
            const state = instanceState(pod);
            return <tr key={pod.name}><td>{pod.name}</td><td>{pod.node || "—"}</td><td><StatusBadge tone={state.tone}>{state.label}</StatusBadge></td><td>{pod.restarts}</td></tr>;
          })}
        </tbody>
      </table>
    </Flex>
  );
}
