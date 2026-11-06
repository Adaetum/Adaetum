import { Ingress, KubeService, Pod, Volume } from "../api";
import { AppCard } from "./apps";

// The route an app is reached by, as an address a person can read.
export interface Address {
  // The name to type, or for a tailnet-only service the ingress's own name.
  host: string;
  // True when it is reachable only over the tailnet (no public or local name).
  tailnet: boolean;
  path: string;
  service: string;
  // True when the ingress terminates TLS.
  secure: boolean;
}

export interface ServiceHop {
  name: string;
  ports: string[];
  // How many of the app's copies it currently sends traffic to.
  copies: number;
}

export interface LinkedApp {
  app: AppCard;
  // The service the connection goes through.
  via: string;
}

// Everything known about how one app is wired into the cluster, from the app's own side:
// who can reach it, what it runs as and where, what it stores, and which other apps it
// is set up to use or is used by.
export interface Trace {
  addresses: Address[];
  services: ServiceHop[];
  copies: Pod[];
  nodes: string[];
  volumes: Volume[];
  // Other apps whose settings name one of this app's services.
  usedBy: LinkedApp[];
  // Services this app's settings name that belong to other apps.
  uses: LinkedApp[];
  // Services its settings name that no app card covers (system or platform services).
  usesOther: string[];
  // Other apps delivered with it (same namespace or the same Argo CD application).
  deliveredWith: AppCard[];
}

// traceFor links by the pod names services list, so it needs no knowledge of labels.
export function traceFor(card: AppCard, cards: AppCard[], services: KubeService[], ingresses: Ingress[]): Trace {
  const podKey = (pod: Pod) => `${pod.namespace}/${pod.name}`;
  const owner = new Map<string, AppCard>();
  for (const other of cards) for (const pod of other.instances) owner.set(podKey(pod), other);

  // A service belongs to the app that owns the pods behind it. A namespace-wide card also
  // keeps services with nothing behind them, which is itself worth seeing.
  const serviceOwner = (service: KubeService): AppCard | undefined => {
    for (const name of service.pods ?? []) {
      const found = owner.get(`${service.namespace}/${name}`);
      if (found) return found;
    }
    return undefined;
  };
  const mine = services.filter((service) => {
    const found = serviceOwner(service);
    return found ? found === card : service.namespace === card.namespace && card.parts.length > 0 && !(service.pods ?? []).length;
  });
  const mineNames = new Set(mine.map((service) => service.name));

  const addresses: Address[] = ingresses
    .filter((ingress) => ingress.namespace === card.namespace)
    .flatMap((ingress) =>
      (ingress.routes ?? [])
        .filter((route) => mineNames.has(route.service))
        .map((route) => ({ host: route.host || ingress.name, tailnet: !route.host, path: route.path ?? "/", service: route.service, secure: ingress.tls || !route.host })),
    );

  const copyNames = new Set(card.instances.map((pod) => pod.name));
  const serviceByKey = new Map(services.map((service) => [`${service.namespace}/${service.name}`, service]));

  const uses: LinkedApp[] = [];
  const usesOther: string[] = [];
  for (const key of new Set(card.instances.flatMap((pod) => pod.calls ?? []))) {
    const service = serviceByKey.get(key);
    const found = service && serviceOwner(service);
    if (found && found !== card) uses.push({ app: found, via: service!.name });
    else if (!found) usesOther.push(key);
  }

  const usedBy: LinkedApp[] = [];
  for (const other of cards) {
    if (other === card) continue;
    const via = [...new Set(other.instances.flatMap((pod) => pod.calls ?? []))].find((key) => mineNames.has(key.split("/")[1]) && key.startsWith(`${card.namespace}/`));
    if (via) usedBy.push({ app: other, via: via.split("/")[1] });
  }

  const deployedBy = new Set(card.deployments.map((deployment) => deployment.name));
  return {
    addresses,
    services: mine.map((service) => ({
      name: service.name,
      ports: service.ports ?? [],
      copies: (service.pods ?? []).filter((name) => copyNames.has(name)).length,
    })),
    // Finished one-off jobs are history, not part of what is running.
    copies: card.instances.filter((pod) => pod.phase !== "Succeeded"),
    nodes: [...new Set(card.instances.map((pod) => pod.node).filter(Boolean))].sort(),
    volumes: card.volumes,
    usedBy,
    uses,
    usesOther,
    deliveredWith: cards.filter((other) => other !== card && other.deployments.some((deployment) => deployedBy.has(deployment.name))),
  };
}
