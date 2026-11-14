import { Autoscaler, Pod } from "../api";
import { AppCard } from "./apps";
import { formatBytes } from "./format";
import { History, HistoryWorkload, TRUSTED_HOURS } from "./historyApi";
import { StorageSizing, sizeStorage } from "./storageSizing";
import { Tone } from "./status";
import { CPU_WASTE, MEMORY_WASTE, materiality } from "./waste";

// "Is this app set up right?" as four scores that add up to one. To a person this is one
// app; underneath, the scores look at its parts and its volumes.
//
//   Resources  what it reserves and may use (35%): requests and limits are set, and sit
//              sensibly apart.
//   Fit        whether those numbers match what it really uses (30%).
//   Scaling    whether the autoscaler it has can do its job (10%); n/a without one.
//   Storage    whether its volumes are the right size: not about to fill, not wildly
//              oversized (25%; see storageSizing.ts); n/a without volumes.
//
// Compute sections are judged per workload (a Deployment, a StatefulSet...) and averaged
// with equal weight, so a small, badly set up part cannot hide behind a big good one.
// Missing numbers are never excused: no request, no limit, or no autoscaler where one
// belongs all cost points, and every cost becomes a plain sentence.
//
// Fit uses the pod's usage over the last days when the agent has recorded it (internal/
// usagehistory): the highest the busiest copy reached for the limits, and the usual use
// for the requests. Without history it falls back to one reading of right now and says
// so. The weights and bands below are judgement, kept in this one file and deliberately
// strict. Sections that do not apply drop out and the rest are re-weighted.

export type SectionKey = "resources" | "fit" | "scaling" | "storage";
export type WorkloadSectionKey = Exclude<SectionKey, "storage">;

export interface Section {
  key: SectionKey;
  label: string;
  // What the section asks, in a few words.
  asks: string;
  weight: number;
  // 0..100, or null when it does not apply (a StatefulSet does not scale out).
  points: number | null;
  // Points lost to shortfalls a person has accepted (see sizeWorkload). They are already out of
  // `points`; they are shown in gray, judged apart from real problems, and left out of `reasons`.
  accepted: number;
  reasons: string[];
  acceptedReasons: string[];
}

export type Grade = "Good" | "Fair" | "Poor";

export interface Amounts {
  cpuMillis?: number;
  memoryBytes?: number;
}

export interface WorkloadSizing {
  name: string;
  kind: string;
  copies: number;
  maxCopies: number;
  usage?: Amounts; // per copy: the highest the busiest copy reached
  typical?: Amounts; // per copy: the usual use (average)
  // What the usage figures are based on.
  basis: { kind: "history" | "now"; hours: number; trusted: boolean };
  request: Amounts; // per copy, undefined where some copy has none
  limit: Amounts;
  autoscaler?: Autoscaler;
  sections: Record<WorkloadSectionKey, { points: number | null; accepted: number; reasons: string[]; acceptedReasons: string[] }>;
  // What to set instead, per copy. Only the values that should change.
  suggestion: { request: Amounts; limit: Amounts; note: string[] };
}

export interface Sizing {
  points: number;
  // The part of the shortfall that is accepted (already out of `points`). Grade and tone judge
  // the score with it added back, so accepted conditions lower the number without raising an alarm.
  acceptedPoints: number;
  grade: Grade;
  tone: Tone;
  sections: Section[];
  workloads: WorkloadSizing[];
  // How the app's volumes score (also section "storage").
  storage: StorageSizing;
  // The few things costing the most points, across sections.
  top: string[];
  // False when the metrics server gave nothing, so Fit could not be judged.
  measured: boolean;
}

export const formatCpu = (millis: number): string => (millis >= 1000 ? `${(millis / 1000).toFixed(1)} cores` : `${Math.round(millis)}m`);
export const formatMemory = formatBytes;

export const toneOf = (points: number): Tone => (points >= 92 ? "green" : points >= 75 ? "amber" : "red");
export const gradeOf = (points: number): Grade => (points >= 92 ? "Good" : points >= 75 ? "Fair" : "Poor");

const SECTIONS: { key: SectionKey; label: string; asks: string; weight: number }[] = [
  { key: "resources", label: "Resources", asks: "Are requests and limits set, and sensible?", weight: 35 },
  { key: "fit", label: "Fit", asks: "Do the numbers match what it really uses?", weight: 30 },
  { key: "scaling", label: "Scaling", asks: "Can it grow when busy, if it should?", weight: 10 },
  { key: "storage", label: "Storage", asks: "Are its volumes the right size?", weight: 25 },
];

// `key` names the acceptable shortfall this reason is about (see acceptedKeys in the agent's inspect package).
type Reason = { cost: number; text: string; key?: string };
const pct = (ratio: number) => `${Math.round(ratio * 100)}%`;
const roundUp = (value: number, step: number) => Math.ceil(value / step) * step;
const MI = 1 << 20;

// Share of copies with a value, squared: half set up is worth a quarter.
const credit = (values: (number | undefined)[]) => {
  const share = values.filter((value) => (value ?? 0) > 0).length / Math.max(values.length, 1);
  return { share, credit: share * share };
};
const average = (values: (number | undefined)[]) => {
  const set = values.filter((value): value is number => (value ?? 0) > 0);
  return set.length ? set.reduce((sum, value) => sum + value, 0) / set.length : undefined;
};

function sizeWorkload(name: string, kind: string, pods: Pod[], autoscaler: Autoscaler | undefined, desired: number, recorded?: HistoryWorkload, acceptedKeys: string[] = []): WorkloadSizing {
  // Shortfalls this workload's owner has accepted (the adaetum.io/accepted annotation). They still
  // cost points; they are only kept out of the problems and shown in gray.
  const accepted = new Set(acceptedKeys);
  const copies = pods.length;
  const maxCopies = Math.max(autoscaler?.max ?? desired, copies);
  const measured = pods.filter((pod) => pod.hasUsage);
  const noun = (what: "cpu" | "memory") => (what === "cpu" ? "CPU" : "memory");

  const memLimit = credit(pods.map((pod) => pod.memoryLimitBytes));
  const cpuLimit = credit(pods.map((pod) => pod.cpuLimitMillis));
  const memRequest = credit(pods.map((pod) => pod.memoryRequestBytes));
  const cpuRequest = credit(pods.map((pod) => pod.cpuRequestMillis));
  const limit: Amounts = { memoryBytes: average(pods.map((pod) => pod.memoryLimitBytes)), cpuMillis: average(pods.map((pod) => pod.cpuLimitMillis)) };
  const request: Amounts = { memoryBytes: average(pods.map((pod) => pod.memoryRequestBytes)), cpuMillis: average(pods.map((pod) => pod.cpuRequestMillis)) };
  // The busiest copy decides: sizing has to hold for each copy, not for their average.
  const now: Amounts | undefined = measured.length
    ? {
        memoryBytes: Math.max(...measured.map((pod) => pod.memoryBytes ?? 0)),
        cpuMillis: Math.max(...measured.map((pod) => pod.cpuMillis ?? 0)),
      }
    : undefined;
  // With a record, the peak is the highest ever seen (or right now, if higher) and the
  // usual use is the average over the record; without one, right now is all there is.
  const usage: Amounts | undefined = recorded
    ? { memoryBytes: Math.max(recorded.memoryPeak, now?.memoryBytes ?? 0), cpuMillis: Math.max(recorded.cpuPeak, now?.cpuMillis ?? 0) }
    : now;
  const typical: Amounts | undefined = recorded ? { memoryBytes: recorded.memoryAvg, cpuMillis: recorded.cpuAvg } : now;
  const basis = { kind: recorded ? ("history" as const) : ("now" as const), hours: recorded?.hours ?? 0, trusted: (recorded?.hours ?? 0) >= TRUSTED_HOURS };

  // ---- Limits (100) ----
  // Memory only. A missing CPU limit is deliberately not a fault: CPU can be shared back out
  // when it is not needed, and a CPU limit throttles an app that is only busy for a moment, so
  // the usual advice is a CPU request and no limit. (A CPU limit that IS set is still judged
  // under Fit, since being held at it slows the app.) Memory cannot be taken back, so a runaway
  // process needs a ceiling.
  const limits: Reason[] = [];
  const limitPoints = 100 * memLimit.credit;
  if (memLimit.share === 0) {
    limits.push({ cost: 100, key: "memory-limit", text: "No memory limit: one runaway process could starve everything else on the server." });
  } else if (memLimit.share < 1) {
    limits.push({ cost: 100 * (1 - memLimit.credit), key: "memory-limit", text: `Some copies have no ${noun("memory")} limit.` });
  }

  // ---- Requests (100): memory set 50, CPU set 38, memory limit against the request 12 ----
  const requests: Reason[] = [];
  let requestPoints = 50 * memRequest.credit + 38 * cpuRequest.credit;
  for (const [what, c, weight] of [["memory", memRequest, 50], ["cpu", cpuRequest, 38]] as const) {
    if (c.share === 0) {
      requests.push({
        cost: weight,
        key: what === "memory" ? "memory-request" : "cpu-request",
        text: what === "memory" ? "No memory request: the scheduler can pack it onto a server without room for it." : "No CPU request: it gets only leftover CPU, and an autoscaler can't measure it.",
      });
    } else if (c.share < 1) {
      requests.push({ cost: weight * (1 - c.credit), key: what === "memory" ? "memory-request" : "cpu-request", text: `Some copies have no ${noun(what)} request.` });
    }
  }
  if (request.memoryBytes && limit.memoryBytes) {
    const ratio = limit.memoryBytes / request.memoryBytes;
    const earned = ratio <= 1.5 ? 12 : ratio <= 2 ? 7 : ratio <= 4 ? 3 : 0;
    requestPoints += earned;
    if (earned < 12) {
      requests.push({ cost: 12 - earned, key: "memory-ratio", text: `The memory limit is ${ratio.toFixed(1)}× the request: it can use far more than was reserved, so the server may run out of memory.` });
    }
  } else {
    requests.push({ cost: 12, key: request.memoryBytes ? "memory-limit" : "memory-request", text: "Memory request and limit aren't both set, so how they relate can't be judged." });
  }

  // ---- Fit (100): memory vs limit 30, memory vs request 25, CPU vs request 25, CPU vs limit 20 (only if one is set) ----
  const fit: Reason[] = [];
  let fitPoints: number | null = null;
  // Weight of parts that do not apply (no CPU limit is allowed), so they neither earn nor cost.
  let notApplicable = 0;
  // Fit points that are zero only because a value is not set, where that shortfall is accepted.
  let fitUnsetAccepted = 0;
  if (usage) {
    fitPoints = 0;
    // `waste` names the reserved-but-unused amount on the low side (ratios under `below`), where
    // how much it costs depends on how much room is really being held back (see materiality).
    const part = (weight: number, c: { credit: number }, ratio: number | null, factor: (ratio: number) => number, say: (ratio: number) => string | null,
      waste?: { below: number; amount: number | undefined; scale: { floor: number; scale: number } }) => {
      if (ratio === null || c.credit === 0) return; // nothing set to fit: already costs points above
      let f = factor(ratio);
      if (waste && ratio < waste.below) {
        // Waste is only charged on a record long enough to trust: one reading can catch an app idle.
        f = basis.trusted && waste.amount !== undefined ? 1 - (1 - f) * materiality(waste.amount, waste.scale) : 1;
      }
      fitPoints! += weight * c.credit * f;
      const text = say(ratio);
      if (f < 1 && text) fit.push({ cost: weight * c.credit * (1 - f), key: waste && ratio < waste.below ? "over-provisioned" : undefined, text });
    };
    if (memLimit.credit === 0 && accepted.has("memory-limit")) fitUnsetAccepted += 30;
    if (memRequest.credit === 0 && accepted.has("memory-request")) fitUnsetAccepted += 25;
    if (cpuRequest.credit === 0 && accepted.has("cpu-request")) fitUnsetAccepted += 25;
    const ratioOf = (used?: number, set?: number) => (used !== undefined && set ? used / set : null);
    // Memory against its limit: near it means being killed. A limit far above the use is not
    // waste (only a request reserves memory), so that side costs nothing.
    part(30, memLimit, ratioOf(usage.memoryBytes, limit.memoryBytes),
      (r) => (r >= 0.9 ? 0 : r >= 0.8 ? 0.3 : r >= 0.7 ? 0.6 : 1),
      (r) => r >= 0.9 ? `Memory ${basis.kind === "history" ? "has reached" : "is at"} ${pct(r)} of its limit: it may be stopped and restarted.`
        : `Getting close to its memory limit (${pct(r)} used).`);
    // Memory against its request: using more than reserved makes it first to be evicted.
    part(25, memRequest, ratioOf(typical?.memoryBytes, request.memoryBytes),
      (r) => (r > 1.2 ? 0 : r > 1 ? 0.5 : r >= 0.6 ? 1 : r >= 0.3 ? 0.8 : r >= 0.15 ? 0.5 : 0.25),
      (r) => r > 1 ? `It usually uses ${pct(r)} of the memory it reserved: it is first to be evicted when the server is short.`
        : `It reserves ${formatMemory(request.memoryBytes!)} but usually uses ${formatMemory(typical!.memoryBytes!)}: that room is wasted for everything else.`,
      { below: 1, amount: request.memoryBytes !== undefined && typical?.memoryBytes !== undefined ? request.memoryBytes - typical.memoryBytes : undefined, scale: MEMORY_WASTE });
    part(25, cpuRequest, ratioOf(typical?.cpuMillis, request.cpuMillis),
      (r) => (r > 1.5 ? 0.2 : r > 1 ? 0.6 : r >= 0.3 ? 1 : r >= 0.1 ? 0.7 : 0.5),
      (r) => r > 1 ? `It usually uses ${pct(r)} of the CPU it reserved: it is relying on spare capacity.`
        : `It reserves ${formatCpu(request.cpuMillis!)} of CPU but usually uses ${formatCpu(typical!.cpuMillis!)}.`,
      { below: 1, amount: request.cpuMillis !== undefined && typical?.cpuMillis !== undefined ? request.cpuMillis - typical.cpuMillis : undefined, scale: CPU_WASTE });
    if (cpuLimit.credit === 0) notApplicable += 20;
    part(20, cpuLimit, ratioOf(usage.cpuMillis, limit.cpuMillis),
      (r) => (r >= 0.9 ? 0 : r >= 0.7 ? 0.5 : 1),
      (r) => `CPU ${basis.kind === "history" ? "has reached" : "is at"} ${pct(r)} of its limit: it is being slowed down.`);
  }

  // ---- Scaling (100), or n/a ----
  const scaling: Reason[] = [];
  let scalingPoints: number | null = null;
  // Scaling is judged where an autoscaler exists. Whether an app SHOULD scale out is
  // declared by giving it one (the repo's rule: stateless apps only), because most apps
  // here (a media server, a database) hold state and are meant to run as one copy; a
  // missing autoscaler is not a defect on its own.
  if (autoscaler && kind === "Deployment") {
    if (autoscaler.max <= autoscaler.min) {
      scalingPoints = 40;
      scaling.push({ cost: 60, text: `The autoscaler can't scale (it is fixed at ${autoscaler.min}).` });
    } else if (!cpuRequest.share) {
      scalingPoints = 20;
      scaling.push({ cost: 80, text: "The autoscaler measures CPU against the request, and there is none, so it can't work properly." });
    } else {
      scalingPoints = 100;
      if (autoscaler.current >= autoscaler.max) {
        scalingPoints -= 30;
        scaling.push({ cost: 30, text: `It is at its maximum of ${autoscaler.max} copies: it can't grow any further.` });
      }
      if (autoscaler.min <= 1) {
        scalingPoints -= 10;
        scaling.push({ cost: 10, text: "It can shrink to a single copy, so a restart then means downtime." });
      }
    }
  }

  // ---- What to set instead (per copy) ----
  const suggestion: WorkloadSizing["suggestion"] = { request: {}, limit: {}, note: [] };
  if (usage?.memoryBytes !== undefined && usage.cpuMillis !== undefined && typical?.memoryBytes !== undefined && typical.cpuMillis !== undefined) {
    // From a record: memory request just above the usual use, limit above the highest
    // ever seen; CPU request above the usual use, limit well above the highest.
    // From one reading: use + a margin, which is a rougher guess.
    const memRequestWant = Math.max(32 * MI, roundUp((basis.kind === "history" ? Math.max(typical.memoryBytes * 1.1, (recorded?.memoryP95Peak ?? 0) * 1.05) : usage.memoryBytes * 1.25), 16 * MI));
    const memLimitWant = roundUp(Math.max(memRequestWant * 1.5, basis.kind === "history" ? usage.memoryBytes * 1.3 : 0), 16 * MI);
    const cpuRequestWant = Math.max(10, roundUp(typical.cpuMillis * 1.5, 10));
    const cpuLimitWant = Math.max(100, roundUp(Math.max(cpuRequestWant * 4, basis.kind === "history" ? usage.cpuMillis * 1.5 : 0), 50));
    // Add what is missing and raise what is too tight, always. Shrink what is generous
    // only with a trusted record: one reading can catch an app idle (a media server only
    // works hard while transcoding).
    const missingOrTight = (have: number | undefined, used: number, want: number, tightAt: number) => (!have || used >= have * tightAt ? want : undefined);
    const generous = (have: number | undefined, want: number, factor: number, floor = 0) => (have && !accepted.has("over-provisioned") && have - want > floor && basis.trusted && have > want * factor ? want : undefined);
    suggestion.request.memoryBytes = missingOrTight(request.memoryBytes, typical.memoryBytes, memRequestWant, 1) ?? generous(request.memoryBytes, memRequestWant, 1.6, MEMORY_WASTE.floor);
    // Limits are only added or raised, never shrunk: a roomy limit reserves nothing, and shrinking
    // it only adds a way to be killed. A CPU limit is not added where there is none.
    suggestion.limit.memoryBytes = missingOrTight(limit.memoryBytes, usage.memoryBytes, memLimitWant, 0.7);
    suggestion.request.cpuMillis = missingOrTight(request.cpuMillis, typical.cpuMillis, cpuRequestWant, 1) ?? generous(request.cpuMillis, cpuRequestWant, 2, CPU_WASTE.floor);
    // An accepted shortfall is not something to fix, so it is not suggested either.
    if (accepted.has("memory-limit") && !limit.memoryBytes) suggestion.limit.memoryBytes = undefined;
    if (accepted.has("memory-request") && !request.memoryBytes) suggestion.request.memoryBytes = undefined;
    if (accepted.has("cpu-request") && !request.cpuMillis) suggestion.request.cpuMillis = undefined;
    suggestion.limit.cpuMillis = limit.cpuMillis ? missingOrTight(limit.cpuMillis, usage.cpuMillis, cpuLimitWant, 0.7) : undefined;
    suggestion.note.push(
      basis.kind === "history"
        ? `Worked out from ${basis.hours} hours of recorded use (${basis.trusted ? "enough to trust" : "not yet enough to shrink anything"}): memory request just above the usual use, limit above the highest it reached; CPU request above the usual use.`
        : "Worked out from one reading of right now: it only adds missing values or raises tight ones.",
    );
    const stillGenerous = !basis.trusted && [
      request.memoryBytes && request.memoryBytes - memRequestWant > MEMORY_WASTE.floor && request.memoryBytes > memRequestWant * 1.6,
      request.cpuMillis && request.cpuMillis - cpuRequestWant > CPU_WASTE.floor && request.cpuMillis > cpuRequestWant * 2,
    ].some(Boolean);
    if (stillGenerous) suggestion.note.push("Some values look generous, but shrinking them needs about two days of recorded use: one reading can catch an app idle.");
  }

  const ranked = (reasons: Reason[]) => reasons.sort((a, b) => b.cost - a.cost).map((reason) => reason.text);
  // Accepted shortfalls come out of the problems and are counted apart (`lost` is their cost).
  const judged = (reasons: Reason[], scale = 1, extra = 0) => {
    const isAccepted = (reason: Reason) => reason.key !== undefined && accepted.has(reason.key);
    return {
      reasons: ranked(reasons.filter((reason) => !isAccepted(reason))),
      acceptedReasons: ranked(reasons.filter(isAccepted)),
      accepted: Math.round((reasons.filter(isAccepted).reduce((sum, reason) => sum + reason.cost, 0) + extra) * scale),
    };
  };
  return {
    name, kind, copies, maxCopies, usage, typical, basis, request, limit, autoscaler,
    sections: {
      // Requests and limits are one score for a person: half each.
      resources: { points: Math.round((limitPoints + requestPoints) / 2), ...judged([...limits, ...requests], 0.5) },
      fit: { points: fitPoints === null ? null : Math.round((fitPoints * 100) / (100 - notApplicable)), ...judged(fit, 100 / (100 - notApplicable), fitUnsetAccepted) },
      scaling: { points: scalingPoints === null ? null : Math.round(scalingPoints), ...judged(scaling) },
    },
    suggestion,
  };
}

export function sizingFor(card: AppCard, autoscalers: Autoscaler[], history: History | null = null): Sizing | null {
  const running = card.instances.filter((pod) => pod.phase === "Running");

  // A pod belongs to the longest workload name it starts with.
  const owner = (pod: Pod) =>
    card.components
      .filter((component) => pod.name.startsWith(`${component.name}-`))
      .sort((a, b) => b.name.length - a.name.length)[0];

  const workloads: WorkloadSizing[] = [];
  for (const component of card.components) {
    const pods = running.filter((pod) => owner(pod) === component);
    if (pods.length === 0) continue;
    const scaler = autoscalers.find(
      (item) => item.namespace === component.namespace && item.targetName === component.name && item.targetKind === component.kind,
    );
    workloads.push(sizeWorkload(component.name, component.kind, pods, scaler, component.desired, history?.byWorkload.get(`${component.namespace}/${component.name}`), component.accepted));
  }
  const storage = sizeStorage(card.volumes, history?.byVolume);
  if (workloads.length === 0 && storage.points === null) return null; // nothing to grade

  const multiple = workloads.length > 1;
  const sections: Section[] = SECTIONS.map((definition) => {
    if (definition.key === "storage") {
      return { ...definition, points: storage.points, accepted: 0, reasons: storage.reasons, acceptedReasons: [] };
    }
    const key = definition.key;
    const scored = workloads.filter((workload) => workload.sections[key].points !== null);
    const points = scored.length
      ? Math.round(scored.reduce((sum, workload) => sum + (workload.sections[key].points as number), 0) / scored.length)
      : null;
    // Reasons from every part, named when there is more than one part.
    const named = (text: string, workload: WorkloadSizing) => (multiple ? `${workload.name}: ${text}` : text);
    const reasons = scored.flatMap((workload) => workload.sections[key].reasons.map((text) => named(text, workload)));
    const acceptedReasons = scored.flatMap((workload) => workload.sections[key].acceptedReasons.map((text) => named(text, workload)));
    const accepted = scored.length ? Math.round(scored.reduce((sum, workload) => sum + workload.sections[key].accepted, 0) / scored.length) : 0;
    return { ...definition, points, accepted, reasons, acceptedReasons };
  });

  // Sections that do not apply drop out and the rest are re-weighted.
  const applicable = sections.filter((section) => section.points !== null);
  const totalWeight = applicable.reduce((sum, section) => sum + section.weight, 0);
  const points = Math.round(applicable.reduce((sum, section) => sum + (section.points as number) * section.weight, 0) / totalWeight);
  const acceptedPoints = Math.min(100 - points, Math.round(applicable.reduce((sum, section) => sum + section.accepted * section.weight, 0) / totalWeight));
  const measured = running.some((pod) => pod.hasUsage);
  // Grade and tone ignore the accepted shortfall: a person has chosen to live with it.
  const judgedPoints = points + acceptedPoints;

  // The few most costly findings across all sections, one per section first.
  const top = sections
    .filter((section) => section.reasons.length > 0 && (section.points ?? 100) < 100)
    .sort((a, b) => (a.points as number) - (b.points as number))
    .slice(0, 2)
    .map((section) => section.reasons[0]);

  return { points, acceptedPoints, grade: gradeOf(judgedPoints), tone: toneOf(judgedPoints), sections, workloads, storage, top, measured };
}
