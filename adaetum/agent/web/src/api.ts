import { authedFetch } from "./lib/auth";
// Mirrors inspect.Report in agent/internal/inspect. Bump in step with
// SchemaVersion on the Go side.
export interface Report {
  schemaVersion: number;
  collectedAt: string;
  host: {
    hostname: string;
    osId: string;
    osVersion: string;
    osName: string;
    kernel: string;
    arch: string;
    failedUnits: string[];
    // About this machine's container runtime only.
    containerImages?: ContainerImages;
  };
  rke2: {
    version?: string;
    services: Record<string, string>;
    nodes?: Node[];
    workloads?: Workload[];
    pods?: Pod[];
    argoApps?: ArgoApp[];
    autoscalers?: Autoscaler[];
    kubeServices?: KubeService[];
    events?: ClusterEvent[];
    volumes?: Volume[];
    storageCapacity?: StorageCapacity;
    ingresses?: Ingress[];
    certificates?: Certificate[];
  };
  metrics?: Metrics;
  errors?: string[];
}

export interface Disk {
  mount: string;
  filesystem: string;
  totalBytes: number;
  usedBytes: number;
  availableBytes: number;
}

export interface Metrics {
  uptimeSeconds: number;
  cpuCount: number;
  load1: number;
  load5: number;
  load15: number;
  memTotalBytes: number;
  memAvailableBytes: number;
  swapTotalBytes: number;
  swapFreeBytes: number;
  disks?: Disk[];
}

export interface Node {
  name: string;
  ready: boolean;
  roles?: string[];
  version?: string;
  internalIp?: string;
  cordoned?: boolean;
  pressure?: string[];
  // What Kubernetes already says about the machine (see inspect.Node).
  allocatableCpuMillis?: number;
  allocatableMemoryBytes?: number;
  hasUsage?: boolean;
  usageCpuMillis?: number;
  usageMemoryBytes?: number;
  osImage?: string;
  kernel?: string;
  containerRuntime?: string;
  readySince?: string;
}

export interface ClusterEvent {
  namespace: string;
  object: string;
  reason: string;
  message: string;
  count: number;
  lastSeen: string;
}

export interface Volume {
  namespace: string;
  name: string;
  phase: string;
  storageClass?: string;
  capacity?: string;
  capacityBytes?: number;
  // Absent when usage was not measured (volumes that are not on Longhorn).
  usedBytes?: number;
  replicas?: number;
  robustness?: string;
  // Whether the storage class lets it be made bigger in place (never smaller).
  expandable?: boolean;
  // The bound PersistentVolume, which is also the volume's name in Longhorn.
  pvName?: string;
}

export interface ContainerImages {
  total: number;
  unused: number;
  // Sizes as the runtime reports them (compressed), good for proportions only.
  totalBytes: number;
  unusedBytes: number;
  // Disk used by the compressed layer copies; absent when not measured. Not part
  // of the kubelet's image figure.
  contentBytes?: number;
}

export interface StorageDisk {
  node: string;
  path: string;
  maxBytes: number;
  reservedBytes: number;
  scheduledBytes: number;
  availableBytes: number;
  schedulable: boolean;
  // Container image store on this disk, when the kubelet reported it.
  imageBytes?: number;
}

export interface StorageCapacity {
  disks: StorageDisk[];
  overProvisionPercent: number;
}

export interface Ingress {
  namespace: string;
  name: string;
  class?: string;
  hosts: string[];
  tls: boolean;
  // Which service each host and path is sent to.
  routes?: Route[];
}

export interface Route {
  host?: string;
  path?: string;
  service: string;
}

// A Kubernetes Service and the pods behind it.
export interface KubeService {
  namespace: string;
  name: string;
  type?: string;
  ports?: string[];
  pods?: string[];
}

export interface Certificate {
  namespace: string;
  name: string;
  ready: boolean;
  notAfter?: string;
  dnsNames?: string[];
  message?: string;
}

export interface ArgoApp {
  namespace: string;
  name: string;
  project: string;
  sync: string;
  health: string;
  revision?: string;
  operation?: string;
  message?: string;
  destNamespace?: string;
  chart?: string;
  chartRepo?: string;
}

export interface Workload {
  kind: string;
  namespace: string;
  name: string;
  desired: number;
  ready: number;
  // Sizing shortfalls the owner accepted (the adaetum.io/accepted annotation): still scored,
  // shown in gray rather than as problems.
  accepted?: string[];
}

export interface Pod {
  namespace: string;
  name: string;
  node: string;
  phase: string;
  readyContainers: number;
  totalContainers: number;
  restarts: number;
  reason?: string;
  // What the pod is using right now, and its ceiling. A limit of 0 or missing means the
  // ceiling is unknown (some container has none).
  hasUsage?: boolean;
  cpuMillis?: number;
  memoryBytes?: number;
  cpuLimitMillis?: number;
  memoryLimitBytes?: number;
  // What it reserves; 0 or missing means some container requests none.
  cpuRequestMillis?: number;
  memoryRequestBytes?: number;
  // Services ("namespace/name") its own settings mention. A hint, not a record of traffic.
  calls?: string[];
}

export interface Autoscaler {
  namespace: string;
  name: string;
  targetKind: string;
  targetName: string;
  min: number;
  max: number;
  current: number;
}

export const SUPPORTED_SCHEMA_VERSION = 1;

export async function fetchReport(signal?: AbortSignal): Promise<Report> {
  const response = await authedFetch("/api/v1/inspect", { signal, quiet: true });
  if (!response.ok) throw new Error(`The agent did not respond correctly (HTTP ${response.status})`);
  return response.json();
}
