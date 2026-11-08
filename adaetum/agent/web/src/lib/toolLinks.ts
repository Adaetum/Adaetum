import { ArgoApp, Ingress, Volume } from "../api";
import { AppCard } from "./apps";

// Links out to the tools that show the same thing in more depth. The addresses are read
// from the cluster's own ingresses, so nothing here is configured: a tool with no web
// address in the cluster simply has no link.
export interface ToolHosts {
  rancher?: string;
  argocd?: string;
  longhorn?: string;
}

// Which ingress belongs to which tool.
const TOOL_NAMESPACES: Record<keyof ToolHosts, string> = { rancher: "cattle-system", argocd: "argocd", longhorn: "longhorn-system" };

// A tool is often reachable on an internal and a public name. Use the one that matches
// how this dashboard was opened, so a link never leaves the network the person is on.
function preferred(hosts: string[], current: string): string | undefined {
  const suffix = (host: string) => host.slice(host.lastIndexOf(".") + 1);
  return hosts.find((host) => suffix(host) === suffix(current)) ?? hosts.find((host) => host.endsWith(".local")) ?? hosts[0];
}

export function toolHosts(ingresses: Ingress[], currentHost: string = window.location.hostname): ToolHosts {
  const found: ToolHosts = {};
  for (const tool of Object.keys(TOOL_NAMESPACES) as (keyof ToolHosts)[]) {
    const hosts = ingresses.filter((ingress) => ingress.namespace === TOOL_NAMESPACES[tool]).flatMap((ingress) => ingress.hosts);
    found[tool] = preferred(hosts, currentHost);
  }
  return found;
}

const RANCHER_KINDS: Record<string, string> = { Deployment: "apps.deployment", StatefulSet: "apps.statefulset", DaemonSet: "apps.daemonset" };

// Rancher's page for the app: its first workload, or the whole namespace.
export function rancherLink(hosts: ToolHosts, card: AppCard): string | undefined {
  if (!hosts.rancher) return undefined;
  const base = `https://${hosts.rancher}/dashboard/c/local/explorer`;
  const first = card.components[0];
  const kind = first && RANCHER_KINDS[first.kind];
  return kind ? `${base}/${kind}/${card.namespace}/${first.name}` : `${base}/namespace/${card.namespace}`;
}

export function argoLink(hosts: ToolHosts, app: ArgoApp): string | undefined {
  return hosts.argocd ? `https://${hosts.argocd}/applications/${encodeURIComponent(app.namespace)}/${encodeURIComponent(app.name)}` : undefined;
}

// Longhorn names its volume after the bound PersistentVolume.
export function longhornLink(hosts: ToolHosts, volume: Volume): string | undefined {
  return hosts.longhorn && volume.pvName ? `https://${hosts.longhorn}/#/volume/${encodeURIComponent(volume.pvName)}` : undefined;
}
