import { Report } from "../api";
import { Destination } from "../app/routes";
import { summarizeApps } from "./apps";
import { formatBytes, plural, shorten } from "./format";
import { argoConverged, certState, daysUntil, nodeProblem, toneForUsage, volumeProblem } from "./status";
import { pressureLabel, warningReason } from "./vocabulary";

export interface AttentionItem {
  id: string;
  severity: "critical" | "warning";
  title: string;
  detail?: string;
  // Where the operator can see the underlying data.
  to: Destination;
}

const RECENT_WARNING_SECONDS = 3600;

function names(values: string[], limit = 3): string {
  const shown = values.slice(0, limit).map((value) => shorten(value)).join(", ");
  return values.length > limit ? `${shown} and ${values.length - limit} more` : shown;
}

// summarizeAttention rolls every view up into the short list an operator should
// read first. Anything not listed here is, by these rules, fine. Critical items
// mean something is broken now; warnings mean something will need attention.
// Wording follows lib/vocabulary.ts: apps, servers, deployments.
export function summarizeAttention(report: Report): AttentionItem[] {
  const items: AttentionItem[] = [];
  const now = new Date(report.collectedAt);
  const { host, rke2 } = report;

  if (host.failedUnits?.length) {
    items.push({
      id: "failed-services",
      severity: "critical",
      title: `${plural(host.failedUnits.length, "system service")} stopped unexpectedly`,
      detail: names(host.failedUnits),
      to: { path: "cluster", params: { focus: "server" } },
    });
  }

  for (const node of rke2.nodes ?? []) {
    if (!node.ready) {
      items.push({ id: `node-${node.name}`, severity: "critical", title: `Server ${node.name} isn't ready`, to: { path: "overview" } });
    } else if (nodeProblem(node)) {
      items.push({
        id: `node-${node.name}`,
        severity: "critical",
        title: `Server ${node.name} is running low on resources`,
        detail: node.pressure?.map(pressureLabel).join(", "),
        to: { path: "overview" },
      });
    }
    if (node.cordoned) {
      items.push({ id: `cordon-${node.name}`, severity: "warning", title: `Server ${node.name} isn't accepting new work`, to: { path: "overview" } });
    }
  }

  const troubled = summarizeApps(rke2.workloads ?? [], rke2.pods ?? []).filter((app) => !app.healthy);
  if (troubled.length) {
    items.push({
      id: "apps",
      severity: "critical",
      title: `${plural(troubled.length, "app")} not fully running`,
      detail: names(troubled.map((app) => app.name)),
      to: { path: "workloads", params: { problems: "1" } },
    });
  }

  const drifting = (rke2.argoApps ?? []).filter((app) => !argoConverged(app));
  if (drifting.length) {
    items.push({
      id: "deployments",
      severity: "warning",
      title: `${plural(drifting.length, "deployment")} out of date or unhealthy`,
      detail: names(drifting.map((app) => app.name)),
      to: { path: "workloads", params: { focus: "deployments" } },
    });
  }

  const badVolumes = (rke2.volumes ?? []).filter(volumeProblem);
  if (badVolumes.length) {
    items.push({
      id: "volumes",
      severity: "critical",
      title: `${plural(badVolumes.length, "storage volume")} with problems`,
      detail: names(badVolumes.map((volume) => volume.name)),
      to: { path: "workloads", params: { focus: "storage" } },
    });
  }

  // A full disk is what actually takes storage down, so each disk is judged on
  // its own: a healthy cluster total can hide one server that is nearly full.
  for (const disk of rke2.storageCapacity?.disks ?? []) {
    const fraction = disk.maxBytes > 0 ? (disk.maxBytes - disk.availableBytes) / disk.maxBytes : 0;
    const tone = toneForUsage(fraction);
    if (tone !== "green") {
      items.push({
        id: `disk-${disk.node}-${disk.path}`,
        severity: tone === "red" ? "critical" : "warning",
        title: `Disk space on ${disk.node} is ${Math.round(fraction * 100)}% full`,
        detail: `${formatBytes(disk.availableBytes)} free`,
        to: { path: "workloads", params: { focus: "storage" } },
      });
    }
  }

  for (const certificate of rke2.certificates ?? []) {
    const state = certState(certificate, now);
    if (state === "not-ready") {
      items.push({
        id: `cert-${certificate.name}`,
        severity: "critical",
        title: `Security certificate ${certificate.name} hasn't been issued`,
        detail: certificate.message,
        to: { path: "workloads", params: { focus: "certificates" } },
      });
    } else if (state === "expiring" && certificate.notAfter) {
      items.push({
        id: `cert-${certificate.name}`,
        severity: "warning",
        title: `Security certificate ${certificate.name} expires in ${plural(daysUntil(certificate.notAfter, now), "day")}`,
        to: { path: "workloads", params: { focus: "certificates" } },
      });
    }
  }

  const recent = (rke2.events ?? []).filter(
    (event) => (now.getTime() - new Date(event.lastSeen).getTime()) / 1000 <= RECENT_WARNING_SECONDS,
  );
  if (recent.length) {
    items.push({
      id: "warnings",
      severity: "warning",
      title: `${plural(recent.length, "warning")} in the last hour`,
      detail: names([...new Set(recent.map((event) => warningReason(event.reason)))]),
      to: { path: "workloads", params: { focus: "warnings" } },
    });
  }

  if (report.errors?.length) {
    items.push({
      id: "collection",
      severity: "warning",
      title: "Some information couldn't be collected",
      detail: "What you see may be incomplete.",
      to: { path: "cluster", params: { focus: "server" } },
    });
  }

  // Critical first, then by insertion order, which already follows impact.
  return items.sort((a, b) => Number(b.severity === "critical") - Number(a.severity === "critical"));
}
