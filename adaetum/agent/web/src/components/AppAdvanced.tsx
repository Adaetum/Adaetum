import { Code, Text } from "@radix-ui/themes";
import { Report } from "../api";
import { AppCard } from "../lib/apps";
import { formatBytes } from "../lib/format";
import { formatCpu, formatMemory } from "../lib/sizing";
import { isLonghornVolume } from "../lib/storage";
import { argoConverged, volumeStatus } from "../lib/status";
import { Trace } from "../lib/trace";
import { componentKind, healthState, instanceState, syncState } from "../lib/vocabulary";
import { Advanced, AdvancedBlock } from "./Advanced";
import { StatusBadge } from "./StatusBadge";

// What each tab shows under its "Advanced" line: the detail the dedicated Apps, Storage
// and Deployments pages carry, scoped to this one app. Cluster-wide figures (disk capacity,
// image cleanup) are not about one app, so they sit under Advanced on the list page.

const dash = <Text color="gray">—</Text>;
const cpu = (millis?: number) => (millis ? formatCpu(millis) : "not set");
const memory = (bytes?: number) => (bytes ? formatMemory(bytes) : "not set");
const percent = (fraction: number) => `${Math.round(fraction * 100)}%`;

export function OverviewAdvanced({ card }: { card: AppCard }) {
  const facts: [string, string][] = [
    ["Namespace", card.namespace],
    ["Components", String(card.components.length)],
    ["Instances", `${card.instancesHealthy} healthy of ${card.instances.length}`],
    ["Restarts", String(card.restarts)],
    ["Volumes", String(card.volumes.length)],
    ["Deployments", card.deployments.map((deployment) => deployment.name).join(", ") || "none recorded"],
    ["Chart", card.chart ? `${card.chart}${card.chartRepo ? ` from ${card.chartRepo}` : ""}` : "not a chart"],
  ];
  return (
    <Advanced>
      <AdvancedBlock title="Identity" note="The names behind this app, for when you need to find it in another tool.">
        <table className="sizing-table">
          <tbody>{facts.map(([name, value]) => <tr key={name}><th scope="row">{name}</th><td>{value}</td></tr>)}</tbody>
        </table>
      </AdvancedBlock>
    </Advanced>
  );
}

export function SizingAdvanced({ card, report }: { card: AppCard; report: Report }) {
  const names = new Set(card.components.map((component) => component.name));
  const autoscalers = (report.rke2.autoscalers ?? []).filter((item) => item.namespace === card.namespace && names.has(item.targetName));
  return (
    <Advanced>
      <AdvancedBlock title="Every copy, right now" note="One reading of what each running copy uses against what it reserves and is allowed.">
        {card.instances.length === 0 ? <Text size="2" color="gray">Nothing is running.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Copy</th><th>Server</th><th>CPU now</th><th>Memory now</th><th>Reserves</th><th>Allowed</th></tr></thead>
            <tbody>
              {card.instances.map((pod) => (
                <tr key={pod.name}>
                  <td>{pod.name}</td>
                  <td>{pod.node || "—"}</td>
                  <td>{pod.hasUsage ? cpu(pod.cpuMillis) : "—"}</td>
                  <td>{pod.hasUsage ? memory(pod.memoryBytes) : "—"}</td>
                  <td>{cpu(pod.cpuRequestMillis)} · {memory(pod.memoryRequestBytes)}</td>
                  <td>{cpu(pod.cpuLimitMillis)} · {memory(pod.memoryLimitBytes)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </AdvancedBlock>
      <AdvancedBlock title="Autoscalers" note="Rules that add or remove copies when the app is busy.">
        {autoscalers.length === 0 ? <Text size="2" color="gray">None: this app runs a fixed number of copies.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Autoscaler</th><th>Scales</th><th>Fewest</th><th>Most</th><th>Now</th></tr></thead>
            <tbody>
              {autoscalers.map((item) => <tr key={item.name}><td>{item.name}</td><td>{item.targetName}</td><td>{item.min}</td><td>{item.max}</td><td>{item.current}</td></tr>)}
            </tbody>
          </table>
        )}
      </AdvancedBlock>
    </Advanced>
  );
}

export function StorageAdvanced({ card }: { card: AppCard }) {
  return (
    <Advanced>
      <AdvancedBlock title="Every volume" note="Disk really used counts every copy Longhorn keeps for safety, so it is the data times the number of copies.">
        {card.volumes.length === 0 ? <Text size="2" color="gray">This app has no volumes.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Volume</th><th>Stored on</th><th>Class</th><th>Size</th><th>In use</th><th>Copies</th><th>Disk really used</th><th>Status</th><th>Volume id</th></tr></thead>
            <tbody>
              {card.volumes.map((volume) => {
                const status = volumeStatus(volume);
                return (
                  <tr key={volume.name}>
                    <td>{volume.name}</td>
                    <td>{isLonghornVolume(volume) ? "Local disks" : "Other storage"}</td>
                    <td>{volume.storageClass ?? dash}{volume.expandable === false ? " (can't grow)" : ""}</td>
                    <td>{volume.capacityBytes ? formatBytes(volume.capacityBytes) : dash}</td>
                    <td>{volume.usedBytes === undefined ? "Not measured" : `${formatBytes(volume.usedBytes)}${volume.capacityBytes ? ` (${percent(volume.usedBytes / volume.capacityBytes)})` : ""}`}</td>
                    <td>{volume.replicas ?? dash}</td>
                    <td>{volume.usedBytes === undefined ? dash : formatBytes(volume.usedBytes * (volume.replicas ?? 1))}</td>
                    <td><StatusBadge tone={status.tone}>{status.label}</StatusBadge></td>
                    <td>{volume.pvName ? <Code size="1" variant="ghost">{volume.pvName}</Code> : dash}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </AdvancedBlock>
    </Advanced>
  );
}

export function TraceAdvanced({ card, report, trace }: { card: AppCard; report: Report; trace: Trace }) {
  const names = new Set(trace.services.map((service) => service.name));
  const services = (report.rke2.kubeServices ?? []).filter((service) => service.namespace === card.namespace && names.has(service.name));
  const rules = (report.rke2.ingresses ?? [])
    .filter((ingress) => ingress.namespace === card.namespace)
    .flatMap((ingress) => (ingress.routes ?? []).filter((route) => names.has(route.service)).map((route) => ({ ingress, route })));
  const calling = card.instances.filter((pod) => (pod.calls ?? []).length > 0);
  return (
    <Advanced>
      <AdvancedBlock title="Services" note="What sends traffic to this app's copies inside the cluster.">
        {services.length === 0 ? <Text size="2" color="gray">None.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Service</th><th>Type</th><th>Ports</th><th>Copies behind it</th></tr></thead>
            <tbody>{services.map((service) => <tr key={service.name}><td>{service.name}</td><td>{service.type ?? "ClusterIP"}</td><td>{(service.ports ?? []).join(", ") || "—"}</td><td>{(service.pods ?? []).join(", ") || "none"}</td></tr>)}</tbody>
          </table>
        )}
      </AdvancedBlock>
      <AdvancedBlock title="Web address rules" note="The ingress rules that send a name to a service.">
        {rules.length === 0 ? <Text size="2" color="gray">None.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Ingress</th><th>Class</th><th>Host</th><th>Path</th><th>To</th><th>Secure</th></tr></thead>
            <tbody>{rules.map(({ ingress, route }) => <tr key={`${ingress.name}/${route.host}/${route.path}/${route.service}`}><td>{ingress.name}</td><td>{ingress.class ?? "—"}</td><td>{route.host || "—"}</td><td>{route.path || "/"}</td><td>{route.service}</td><td>{ingress.tls ? "yes" : "no"}</td></tr>)}</tbody>
          </table>
        )}
      </AdvancedBlock>
      <AdvancedBlock title="Services named in each copy's settings" note="Where the connections shown above were found.">
        {calling.length === 0 ? <Text size="2" color="gray">No copy names another service.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Copy</th><th>Names</th></tr></thead>
            <tbody>{calling.map((pod) => <tr key={pod.name}><td>{pod.name}</td><td>{(pod.calls ?? []).join(", ")}</td></tr>)}</tbody>
          </table>
        )}
      </AdvancedBlock>
    </Advanced>
  );
}

export function DetailsAdvanced({ card }: { card: AppCard }) {
  return (
    <Advanced>
      <AdvancedBlock title="Components in full">
        {card.components.length === 0 ? <Text size="2" color="gray">None.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Component</th><th>Type</th><th>Kind</th><th>Copies running</th></tr></thead>
            <tbody>{card.components.map((component) => <tr key={`${component.kind}/${component.name}`}><td>{component.name}</td><td>{componentKind(component.kind)}</td><td><Code size="1" variant="ghost">{component.kind}</Code></td><td>{component.ready} of {component.desired}</td></tr>)}</tbody>
          </table>
        )}
      </AdvancedBlock>
      <AdvancedBlock title="Instances in full">
        {card.instances.length === 0 ? <Text size="2" color="gray">None.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Instance</th><th>Server</th><th>Ready</th><th>Restarts</th><th>Status</th><th>Reason</th></tr></thead>
            <tbody>
              {card.instances.map((pod) => {
                const state = instanceState(pod);
                return <tr key={pod.name}><td>{pod.name}</td><td>{pod.node || "—"}</td><td>{pod.readyContainers} of {pod.totalContainers}</td><td>{pod.restarts}</td><td><StatusBadge tone={state.tone}>{state.label}</StatusBadge></td><td>{pod.reason ?? "—"}</td></tr>;
              })}
            </tbody>
          </table>
        )}
      </AdvancedBlock>
    </Advanced>
  );
}

export function DeploymentsAdvanced({ card }: { card: AppCard }) {
  return (
    <Advanced>
      <AdvancedBlock title="Deployments in full" note="Raw Argo CD values are shown under the plain labels.">
        {card.deployments.length === 0 ? <Text size="2" color="gray">None recorded.</Text> : (
          <table className="sizing-table">
            <thead><tr><th>Deployment</th><th>Project</th><th>Into</th><th>Matches repository</th><th>Health</th><th>Activity</th><th>Version</th></tr></thead>
            <tbody>
              {card.deployments.map((deployment) => {
                const sync = syncState(deployment.sync);
                const health = healthState(deployment.health);
                return (
                  <tr key={deployment.name}>
                    <td>{deployment.name}{!argoConverged(deployment) && deployment.message && <Text as="div" size="1" color="red">{deployment.message}</Text>}</td>
                    <td>{deployment.project}</td>
                    <td>{deployment.destNamespace ?? "—"}</td>
                    <td><StatusBadge tone={sync.tone}>{sync.label}</StatusBadge> <Code size="1" variant="ghost">{deployment.sync}</Code></td>
                    <td><StatusBadge tone={health.tone}>{health.label}</StatusBadge> <Code size="1" variant="ghost">{deployment.health}</Code></td>
                    <td>{deployment.operation ?? "—"}</td>
                    <td>{deployment.revision ? <Code size="1" variant="ghost">{deployment.revision}</Code> : dash}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </AdvancedBlock>
    </Advanced>
  );
}
