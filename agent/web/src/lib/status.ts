import { ArgoApp, Certificate, Node, Pod, Volume, Workload } from "../api";

// Radix Themes color names used to express status. Keeping the mapping here
// means "what counts as unhealthy" is defined once for the whole UI.
export type Tone = "green" | "amber" | "red" | "gray" | "blue";

export const CERT_WARN_DAYS = 30;
const MS_PER_DAY = 86_400_000;

export function podHealthy(pod: Pod): boolean {
  if (pod.phase === "Succeeded") return true;
  return pod.phase === "Running" && pod.readyContainers === pod.totalContainers;
}

export function workloadReady(workload: Workload): boolean {
  return workload.ready >= workload.desired;
}

export function argoConverged(app: ArgoApp): boolean {
  return app.sync === "Synced" && app.health === "Healthy";
}

export function volumeProblem(volume: Volume): boolean {
  return volume.phase !== "Bound" || volume.robustness === "degraded" || volume.robustness === "faulted";
}

export interface VolumeStatus {
  label: string;
  tone: Tone;
  // Higher is worse; used to find an app's worst volume and to sort problems first.
  rank: number;
}

// volumeStatus turns Kubernetes and Longhorn states into the few words an
// operator needs. "unknown" robustness means Longhorn has no pod attached to
// the volume, which is normal and not a fault.
export function volumeStatus(volume: Volume): VolumeStatus {
  if (volume.phase === "Lost") return { label: "Lost", tone: "red", rank: 4 };
  if (volume.phase !== "Bound") return { label: "Waiting for storage", tone: "amber", rank: 2 };
  switch (volume.robustness) {
    case "faulted":
      return { label: "Failed", tone: "red", rank: 4 };
    case "degraded":
      return { label: "Degraded", tone: "amber", rank: 3 };
    case "healthy":
      return { label: "Healthy", tone: "green", rank: 0 };
    case "unknown":
      // Not attached to a pod: normal, so it must not outrank healthy volumes.
      return { label: "Idle", tone: "gray", rank: 0 };
    default:
      return { label: "Ready", tone: "green", rank: 0 };
  }
}

export function nodeProblem(node: Node): boolean {
  return !node.ready || Boolean(node.pressure?.length);
}

export function daysUntil(iso: string, now: Date): number {
  return Math.floor((new Date(iso).getTime() - now.getTime()) / MS_PER_DAY);
}

export type CertState = "not-ready" | "expiring" | "ok";

export function certState(certificate: Certificate, now: Date): CertState {
  if (!certificate.ready) return "not-ready";
  if (certificate.notAfter && daysUntil(certificate.notAfter, now) <= CERT_WARN_DAYS) return "expiring";
  return "ok";
}

// Thresholds flag "look at this"; they are not an SLO.
export function toneForUsage(fraction: number): Tone {
  if (fraction >= 0.9) return "red";
  if (fraction >= 0.75) return "amber";
  return "green";
}
