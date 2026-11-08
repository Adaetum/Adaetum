import { ArgoApp, Pod, Volume, Workload } from "../api";
import { plural } from "./format";
import { podHealthy, workloadReady } from "./status";

// An app is a namespace and everything in it, which is how this stack is laid
// out. Grouping here lets every view talk about apps instead of pods and
// deployments.
export interface AppSummary {
  name: string;
  components: Workload[];
  instances: Pod[];
  componentsReady: number;
  instancesHealthy: number;
  restarts: number;
  healthy: boolean;
  // One sentence on what is wrong; empty when the app is healthy.
  problem: string;
}

export function summarizeApps(workloads: Workload[], pods: Pod[]): AppSummary[] {
  const apps = new Map<string, { components: Workload[]; instances: Pod[] }>();
  const entry = (name: string) => {
    let value = apps.get(name);
    if (!value) apps.set(name, (value = { components: [], instances: [] }));
    return value;
  };
  workloads.forEach((workload) => entry(workload.namespace).components.push(workload));
  pods.forEach((pod) => entry(pod.namespace).instances.push(pod));

  return [...apps.entries()].map(([name, { components, instances }]) => {
    const componentsReady = components.filter(workloadReady).length;
    const instancesHealthy = instances.filter(podHealthy).length;
    const notReady = components.length - componentsReady;
    const unhealthy = instances.length - instancesHealthy;

    const problems: string[] = [];
    if (notReady > 0) problems.push(`${plural(notReady, "component")} below the wanted number of copies`);
    if (unhealthy > 0) problems.push(`${plural(unhealthy, "instance")} not healthy`);

    return {
      name,
      components,
      instances,
      componentsReady,
      instancesHealthy,
      restarts: instances.reduce((total, pod) => total + pod.restarts, 0),
      healthy: problems.length === 0,
      problem: problems.join(", "),
    };
  });
}

// ---- Apps for the cards -----------------------------------------------------
//
// The cards show what a person thinks of as an app, not a namespace. Two decisions
// are made here, in one place, because nothing in the cluster says them:
//   - SYSTEM apps (the platform itself) are left out unless they need attention.
//   - BROKEN_OUT namespaces hold several unrelated apps (a media stack), so each
//     component becomes its own card instead of one card for the whole namespace.

// Namespaces that are the platform, not something a person runs.
const SYSTEM_NAMESPACES = new Set([
  "default", "argocd", "ansible", "cert-manager", "cdi", "cloudflared", "external-secrets",
  "ingress", "kubescape", "kubevirt", "kubewarden", "longhorn-system", "openbao", "reloader",
  "seaweedfs-csi-driver", "tailscale", "tigera-operator", "calico-system", "autoscaling-system",
]);
const SYSTEM_PATTERN = /^(kube-|cattle-|fleet-)|-system$/;

// Namespaces whose components are separate apps.
const BROKEN_OUT = new Set(["media", "ai-dev"]);

// Components that support an app rather than being one.
const SUPPORTING = /^(.*-)?(postgres(ql)?|redis(-master)?|mariadb|mysql|valkey|memcached|mongodb)$/;

export const isSystemNamespace = (namespace: string): boolean =>
  SYSTEM_NAMESPACES.has(namespace) || SYSTEM_PATTERN.test(namespace);

// One card.
export interface AppCard extends AppSummary {
  // The namespace it lives in (the Components tab filters on it).
  namespace: string;
  system: boolean;
  // What the logo lookup may use besides the name.
  chart?: string;
  chartRepo?: string;
  parts: string[];
  // Its volumes, and the deployments (Argo CD applications) that deliver it. To a person
  // these are all just "the app"; the page shows them as sections of one app.
  volumes: Volume[];
  deployments: ArgoApp[];
}

// The id of a card in the address bar and in lists: unique across namespaces.
export const cardKey = (card: AppCard): string => `${card.namespace}/${card.name}`;

export function summarizeAppCards(workloads: Workload[], pods: Pod[], argoApps: ArgoApp[] = [], volumes: Volume[] = []): AppCard[] {
  const cards: AppCard[] = [];
  for (const app of summarizeApps(workloads, pods)) {
    const system = isSystemNamespace(app.name);
    const deployed = argoApps.filter((a) => a.destNamespace === app.name);
    if (!BROKEN_OUT.has(app.name)) {
      const chart = deployed.find((a) => a.chart);
      cards.push({
        ...app, namespace: app.name, system, chart: chart?.chart, chartRepo: chart?.chartRepo,
        parts: app.components.map((c) => c.name),
        volumes: volumes.filter((volume) => volume.namespace === app.name),
        deployments: deployed,
      });
      continue;
    }

    // Each component is an app; its name loses the stack's prefix ("media-stack-bazarr" -> "bazarr").
    const prefixes = [...deployed.map((a) => `${a.name}-`), `${app.name}-`].sort((a, b) => b.length - a.length);
    const displayName = (name: string) => {
      const prefix = prefixes.find((p) => name.startsWith(p) && name.length > p.length);
      return prefix ? name.slice(prefix.length) : name;
    };
    const owned = app.components.filter((c) => !SUPPORTING.test(displayName(c.name)));
    // A volume belongs to the part whose name is the longest prefix of the volume's name
    // ("media-stack-plex-config" -> plex). Any left over are shared by the stack.
    const nsVolumes = volumes.filter((volume) => volume.namespace === app.name);
    const volumeOwner = (volume: Volume) =>
      owned.filter((c) => volume.name.startsWith(`${c.name}-`)).sort((a, b) => b.name.length - a.name.length)[0];
    for (const component of owned) {
      // A pod belongs to the longest component name it starts with, so
      // "octo-ytdlp-shim" pods are not counted for "octo".
      const mine = app.instances.filter((pod) => {
        const best = owned
          .filter((other) => pod.name.startsWith(`${other.name}-`))
          .sort((a, b) => b.name.length - a.name.length)[0];
        return best === component;
      });
      const healthy = workloadReady(component) && mine.every(podHealthy);
      const restarts = mine.reduce((total, pod) => total + pod.restarts, 0);
      cards.push({
        name: displayName(component.name),
        namespace: app.name,
        system,
        components: [component],
        instances: mine,
        componentsReady: workloadReady(component) ? 1 : 0,
        instancesHealthy: mine.filter(podHealthy).length,
        restarts,
        healthy,
        problem: healthy ? "" : workloadReady(component) ? `${plural(mine.filter((p) => !podHealthy(p)).length, "instance")} not healthy` : "Fewer copies running than wanted",
        parts: [],
        volumes: nsVolumes.filter((volume) => volumeOwner(volume) === component),
        deployments: deployed,
      });
    }
    const shared = nsVolumes.filter((volume) => !volumeOwner(volume));
    if (shared.length > 0) {
      cards.push({
        name: `${app.name} (shared)`, namespace: app.name, system, components: [], instances: [], componentsReady: 0,
        instancesHealthy: 0, restarts: 0, healthy: true, problem: "", parts: [], volumes: shared, deployments: deployed,
      });
    }
  }
  return cards;
}
