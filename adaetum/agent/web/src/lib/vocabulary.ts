import { Pod } from "../api";
import { podHealthy, Tone } from "./status";

// Plain-language names for Kubernetes and Argo CD concepts. This is the single
// place that decides what the UI calls things; pages must not print raw
// Kubernetes terms. Raw values stay available in tooltips and the raw report
// for operators who need them.
//
// Vocabulary:   app        a namespace and everything running in it
//               component  a Deployment, StatefulSet, or DaemonSet
//               instance   a pod
//               server     a cluster node

export interface Labeled {
  label: string;
  tone: Tone;
  // Higher is worse; used to sort problems first.
  rank: number;
}

// humanize turns "FailedMount" into "Failed mount" for reasons with no entry.
export function humanize(value: string): string {
  const spaced = value.replace(/([a-z0-9])([A-Z])/g, "$1 $2").trim();
  return spaced ? spaced[0].toUpperCase() + spaced.slice(1).toLowerCase() : "Unknown";
}

// Deployment status ("does the cluster match the repository?").
export function syncState(status: string): Labeled {
  switch (status) {
    case "Synced":
      return { label: "Up to date", tone: "green", rank: 0 };
    case "OutOfSync":
      return { label: "Out of date", tone: "amber", rank: 2 };
    default:
      return { label: "Unknown", tone: "gray", rank: 1 };
  }
}

export function healthState(status: string): Labeled {
  switch (status) {
    case "Healthy":
      return { label: "Healthy", tone: "green", rank: 0 };
    case "Progressing":
      return { label: "Starting", tone: "amber", rank: 1 };
    case "Suspended":
      return { label: "Paused", tone: "gray", rank: 1 };
    case "Degraded":
      return { label: "Unhealthy", tone: "red", rank: 3 };
    case "Missing":
      return { label: "Missing", tone: "red", rank: 3 };
    default:
      return { label: "Unknown", tone: "gray", rank: 1 };
  }
}

const POD_REASONS: Record<string, Labeled> = {
  CrashLoopBackOff: { label: "Crashing repeatedly", tone: "red", rank: 4 },
  ImagePullBackOff: { label: "Can't download image", tone: "red", rank: 4 },
  ErrImagePull: { label: "Can't download image", tone: "red", rank: 4 },
  ContainerCreating: { label: "Starting", tone: "amber", rank: 2 },
  PodInitializing: { label: "Starting", tone: "amber", rank: 2 },
  OOMKilled: { label: "Ran out of memory", tone: "red", rank: 4 },
  Evicted: { label: "Stopped to free resources", tone: "red", rank: 4 },
  Completed: { label: "Finished", tone: "gray", rank: 0 },
  Error: { label: "Error", tone: "red", rank: 4 },
};

// An instance's state in words. A reason (why it is stuck) is more useful than
// its phase, so it wins when present.
export function instanceState(pod: Pod): Labeled {
  if (pod.phase === "Succeeded") return { label: "Finished", tone: "gray", rank: 0 };
  if (podHealthy(pod)) return { label: "Running", tone: "green", rank: 0 };
  if (pod.reason && POD_REASONS[pod.reason]) return POD_REASONS[pod.reason];
  switch (pod.phase) {
    case "Pending":
      return { label: "Starting", tone: "amber", rank: 2 };
    case "Failed":
      return { label: "Failed", tone: "red", rank: 4 };
    case "Running":
      return { label: "Not ready", tone: "amber", rank: 3 };
    default:
      return { label: pod.reason ? humanize(pod.reason) : "Unknown", tone: "gray", rank: 1 };
  }
}

const COMPONENT_KINDS: Record<string, string> = {
  Deployment: "Standard",
  StatefulSet: "Keeps its own data",
  DaemonSet: "One per server",
};

export const componentKind = (kind: string) => COMPONENT_KINDS[kind] ?? kind;

const NODE_ROLES: Record<string, string> = {
  "control-plane": "Controller",
  master: "Controller",
  etcd: "Cluster data",
  worker: "Worker",
};

const PRESSURE: Record<string, string> = {
  MemoryPressure: "Low on memory",
  DiskPressure: "Low on disk space",
  PIDPressure: "Too many processes",
};

export const pressureLabel = (condition: string) => PRESSURE[condition] ?? humanize(condition);

export const serverRole = (role: string) => NODE_ROLES[role] ?? humanize(role);

// Why Kubernetes raised a warning, in words an operator can act on.
const EVENT_REASONS: Record<string, string> = {
  Unhealthy: "Health check failed",
  ProbeWarning: "Health check warning",
  BackOff: "Restarting repeatedly",
  FailedMount: "Couldn't attach storage",
  FailedAttachVolume: "Couldn't attach storage",
  FailedScheduling: "No room to run",
  FailedCreate: "Couldn't create",
  Failed: "Failed",
  Evicted: "Stopped to free resources",
  OOMKilling: "Ran out of memory",
  NodeNotReady: "Server not ready",
  ImagePullBackOff: "Can't download image",
  ErrImagePull: "Can't download image",
  FailedGetResourceMetric: "Couldn't read usage numbers",
  FailedComputeMetricsReplicas: "Couldn't work out how many copies to run",
  SystemOOM: "Server ran out of memory",
};

export const warningReason = (reason: string) => EVENT_REASONS[reason] ?? humanize(reason);

const ARCHITECTURES: Record<string, string> = {
  amd64: "Intel/AMD 64-bit",
  arm64: "ARM 64-bit",
};

export const processorType = (arch: string) => ARCHITECTURES[arch] ?? arch;

// RKE2 runs one of two services per machine. A machine that is a controller
// runs the server one and never the agent one, so "inactive" is expected.
export function clusterService(unit: string, state: string): { name: string; state: string; tone: Tone } {
  const active = state === "active";
  if (unit === "rke2-server") {
    return { name: "Cluster controller", state: active ? "Running" : "Not running", tone: active ? "green" : "gray" };
  }
  if (unit === "rke2-agent") {
    return { name: "Cluster worker", state: active ? "Running" : "Not used on this server", tone: active ? "green" : "gray" };
  }
  return { name: unit, state: active ? "Running" : humanize(state), tone: active ? "green" : "gray" };
}

// The "Kubernetes object kind" prefix of an event target ("Pod/name") means
// nothing to most readers. Instances are the common case, so they are unmarked.
export function eventTarget(object: string): { name: string; kind?: string } {
  const [kind, ...rest] = object.split("/");
  const name = rest.join("/");
  if (!name) return { name: object };
  if (kind === "Pod") return { name };
  const KINDS: Record<string, string> = { Node: "Server", PersistentVolumeClaim: "Storage", Deployment: "Component", StatefulSet: "Component", DaemonSet: "Component" };
  return { name, kind: KINDS[kind] ?? kind };
}
