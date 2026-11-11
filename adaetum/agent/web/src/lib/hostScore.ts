import { Node } from "../api";
import { Grade, gradeOf, toneOf } from "./sizing";
import { Tone } from "./status";

// "Is this machine looked after?" as three scores that add up to one, in the same shape as
// the workload setup score so both read the same way on screen.
//
// Everything here is what Kubernetes already says about the machine (its conditions, its
// metrics, its versions). Nothing is measured a second time on the machine itself, so the
// score can never disagree with the cluster and works the moment a machine joins.
//
//   Health     whether the cluster considers it fit for work (50%): ready, not cordoned, and
//              no memory, disk or process pressure.
//   Resources  whether it has room (30%): how much of its CPU and memory pods are using.
//              Drops out when there is no metrics server.
//   Versions   whether it matches its peers (20%): the same Kubernetes version as the newest
//              machine, and an agent that is not out of date.
//
// A section with nothing to judge drops out and the rest re-weight. Every point lost
// becomes one plain sentence with what to do about it. The weights and bands are judgement,
// kept in this one file and deliberately strict.

export type HostSectionKey = "health" | "resources" | "versions";

export interface HostSection {
  key: HostSectionKey;
  label: string;
  asks: string;
  weight: number;
  points: number | null;
  reasons: string[];
}

// One thing worth fixing, in words a person can read, with what to ask the assistant.
export interface HostIssue {
  section: HostSectionKey;
  cost: number;
  text: string;
  ask: string;
}

export interface HostScore {
  points: number;
  grade: Grade;
  tone: Tone;
  sections: HostSection[];
  issues: HostIssue[];
  // The few things costing the most points.
  top: string[];
}

const SECTIONS: { key: HostSectionKey; label: string; asks: string; weight: number }[] = [
  { key: "health", label: "Health", asks: "Does the cluster consider it fit for work?", weight: 50 },
  { key: "resources", label: "Resources", asks: "Does it have room to spare?", weight: 30 },
  { key: "versions", label: "Versions", asks: "Does it match the rest of the cluster?", weight: 20 },
];

const clamp = (points: number) => Math.max(0, Math.min(100, Math.round(points)));
const pct = (fraction: number) => `${Math.round(fraction * 100)}%`;

// What each kind of pressure means to a person.
const PRESSURE: Record<string, { text: string; ask: string }> = {
  DiskPressure: { text: "The disk is nearly full, so Kubernetes is clearing space and may stop new work here.", ask: "free up disk space on this machine, starting with the safest clean-up" },
  MemoryPressure: { text: "The machine is running out of memory, so Kubernetes may stop programs on it.", ask: "work out what is using so much memory" },
  PIDPressure: { text: "The machine has too many processes running.", ask: "find what is starting so many processes" },
};

// Compare "v1.35.6+rke2r1"-style versions by their numbers.
const versionParts = (version: string) => (version.match(/\d+/g) ?? []).map(Number);
const compareVersions = (a: string, b: string) => {
  const left = versionParts(a), right = versionParts(b);
  for (let i = 0; i < Math.max(left.length, right.length); i++) {
    const difference = (left[i] ?? 0) - (right[i] ?? 0);
    if (difference !== 0) return difference;
  }
  return 0;
};

// `peers` is every machine in the cluster, so a version is judged against the newest one.
export function scoreHost(node: Node | undefined, peers: Node[], agentOutdated: boolean): HostScore | null {
  if (!node) return null;
  const issues: HostIssue[] = [];
  const sections: HostSection[] = SECTIONS.map((section) => ({ ...section, points: null as number | null, reasons: [] }));
  const byKey = Object.fromEntries(sections.map((section) => [section.key, section])) as Record<HostSectionKey, HostSection>;
  const reason = (key: HostSectionKey, cost: number, text: string, ask: string) => {
    byKey[key].reasons.push(text);
    issues.push({ section: key, cost, text, ask });
  };

  // Health
  {
    let cost = 0;
    const lose = (amount: number, text: string, ask: string) => { cost += amount; reason("health", amount, text, ask); };
    if (!node.ready) lose(70, "Kubernetes says this machine isn't ready, so it isn't running any work.", "find out why this node is NotReady and get it back");
    for (const kind of node.pressure ?? []) {
      const known = PRESSURE[kind];
      if (known) lose(25, known.text, known.ask);
    }
    if (node.cordoned) lose(15, "The machine is paused (cordoned), so no new work is placed on it.", "check whether this node should still be cordoned and uncordon it if the maintenance is finished");
    byKey.health.points = clamp(100 - cost);
  }

  // Resources
  if (node.hasUsage) {
    const cpu = node.allocatableCpuMillis && node.usageCpuMillis !== undefined ? node.usageCpuMillis / node.allocatableCpuMillis : undefined;
    const memory = node.allocatableMemoryBytes && node.usageMemoryBytes !== undefined ? node.usageMemoryBytes / node.allocatableMemoryBytes : undefined;
    if (cpu !== undefined || memory !== undefined) {
      // Comfortable to 70% used; nothing left to spare by 95%.
      const room = (used: number) => (used <= 0.7 ? 100 : used >= 0.95 ? 0 : (100 * (0.95 - used)) / 0.25);
      const scores = [cpu, memory].filter((used): used is number => used !== undefined).map(room);
      byKey.resources.points = clamp(Math.min(...scores));
      if (cpu !== undefined && cpu > 0.8) reason("resources", 100 - room(cpu), `It is using ${pct(cpu)} of its processing power.`, "work out what is keeping this machine busy and whether work should move elsewhere");
      if (memory !== undefined && memory > 0.8) reason("resources", 100 - room(memory), `It is using ${pct(memory)} of its memory.`, "work out what is using so much memory and whether work should move elsewhere");
    }
  }

  // Versions
  {
    let cost = 0;
    const newest = peers.map((peer) => peer.version).filter((version): version is string => !!version).sort(compareVersions).pop();
    if (node.version && newest && compareVersions(node.version, newest) < 0) {
      cost += 60;
      reason("versions", 60, `It runs Kubernetes ${node.version}, while another machine runs ${newest}.`, `bring this machine up to Kubernetes ${newest} to match the rest of the cluster, one node at a time`);
    }
    if (agentOutdated) {
      cost += 40;
      reason("versions", 40, "Its Adaetum agent is older than the others.", "update the Adaetum agent on this machine");
    }
    if (node.version || agentOutdated) byKey.versions.points = clamp(100 - cost);
  }

  const scored = sections.filter((section) => section.points !== null);
  if (scored.length === 0) return null;
  const weight = scored.reduce((sum, section) => sum + section.weight, 0);
  const points = clamp(scored.reduce((sum, section) => sum + (section.points as number) * section.weight, 0) / weight);
  issues.sort((a, b) => b.cost - a.cost);
  return { points, grade: gradeOf(points), tone: toneOf(points), sections, issues, top: issues.slice(0, 3).map((item) => item.text) };
}

// The question the Fix button hands to the assistant: the score, every finding, and
// the rule that the assistant explains and guides rather than changing the machine itself.
export function hostFixAsk(name: string, score: HostScore, only?: HostIssue): string {
  const list = (only ? [only] : score.issues).map((item) => `- ${item.text} (to fix: ${item.ask})`).join("\n");
  return [
    `The server "${name}" scores ${score.points}/100 (${score.grade}) for how well it is looked after.`,
    only ? "Please help me fix this one problem:" : "Please help me fix these problems, most important first:",
    list,
    "Explain each step in plain words, say what could go wrong, and check before anything that could interrupt the cluster. You can't change the machine yourself, so give me the exact steps or tell me which automation to run.",
  ].join("\n");
}
