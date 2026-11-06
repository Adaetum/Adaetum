import { Volume } from "../api";
import { HistoryVolume } from "./historyApi";
import { formatBytes } from "./format";

// "Is this volume the right size?" The two ways to be wrong are not equal. Too small is an
// outage waiting to happen (writes fail when it fills), so it is scored hard. Too large is
// waste: Longhorn volumes are thin, so it costs no disk today, but it counts against what
// the cluster may schedule, and a volume can be grown later but never shrunk, which is why
// the sizing aims lean. Growth over the recorded days says how long until it fills.
//
// Weights and bands are judgement, kept in this one file.

const GIB = 1 << 30;

export interface VolumeSizing {
  name: string;
  capacityBytes: number;
  // Absent when the volume is not measured (not on Longhorn).
  usedBytes?: number;
  replicas?: number;
  expandable?: boolean;
  // Bytes added per day from the record, and days until full at that rate.
  growthPerDay?: number;
  daysToFull?: number;
  // 0..100, or null when there is nothing to judge.
  points: number | null;
  reasons: string[];
  // What to do about it, if anything.
  suggestion?: { kind: "grow" | "shrink"; newBytes: number; why: string };
}

// Sizes people would actually choose, so a suggestion reads "20 GiB" and not "17.3 GiB".
const NICE = [1, 2, 3, 5, 8, 10, 16, 20, 32, 50, 64, 100, 128, 200, 256, 500, 1000, 2000].map((value) => value * GIB);
export const niceSize = (bytes: number): number => NICE.find((size) => size >= bytes) ?? Math.ceil(bytes / GIB) * GIB;

export const formatSize = (bytes: number): string => formatBytes(bytes).replace("iB", "iB");

export function sizeVolume(volume: Volume, recorded?: HistoryVolume): VolumeSizing {
  const capacity = volume.capacityBytes ?? 0;
  const result: VolumeSizing = {
    name: volume.name, capacityBytes: capacity, usedBytes: volume.usedBytes, replicas: volume.replicas,
    expandable: volume.expandable, points: null, reasons: [],
  };
  if (capacity === 0 || volume.usedBytes === undefined) return result; // not measured

  const used = volume.usedBytes;
  const fullness = used / capacity;
  const growth = recorded && recorded.spanHours >= 24 ? recorded.growthPerDay : undefined;
  if (growth !== undefined) result.growthPerDay = growth;
  const daysToFull = growth !== undefined && growth > 0 ? Math.max(0, (capacity - used) / growth) : undefined;
  if (daysToFull !== undefined) result.daysToFull = daysToFull;

  const problems: { cost: number; text: string }[] = [];
  let points = 100;
  const lose = (to: number, text: string) => {
    if (to < points) {
      problems.push({ cost: points - to, text });
      points = to;
    }
  };

  // ---- Too small ----
  const percent = `${Math.round(fullness * 100)}%`;
  if (fullness >= 0.97) lose(0, `It is ${percent} full: writes will fail the moment it fills.`);
  else if (fullness >= 0.9) lose(20, `It is ${percent} full: very little room is left.`);
  else if (fullness >= 0.85) lose(40, `It is ${percent} full.`);
  else if (fullness >= 0.75) lose(65, `It is ${percent} full: getting tight.`);
  else if (fullness >= 0.65) lose(85, `It is ${percent} full.`);
  if (daysToFull !== undefined) {
    const when = daysToFull < 1 ? "within a day" : `in about ${Math.round(daysToFull)} days`;
    if (daysToFull < 7) lose(20, `At its recent pace (${formatBytes(growth!)} a day) it will be full ${when}.`);
    else if (daysToFull < 14) lose(40, `At its recent pace (${formatBytes(growth!)} a day) it will be full ${when}.`);
    else if (daysToFull < 30) lose(65, `At its recent pace (${formatBytes(growth!)} a day) it will be full ${when}.`);
    else if (daysToFull < 60) lose(85, `At its recent pace (${formatBytes(growth!)} a day) it will be full ${when}.`);
  }

  // ---- Too large (only worth noting from 2 GiB up, and not if it is filling) ----
  const projected90 = used + Math.max(growth ?? 0, 0) * 90;
  const lean = niceSize(Math.max(used * 3, projected90 * 1.5, GIB));
  const fillingFast = projected90 >= capacity * 0.5;
  if (capacity >= 2 * GIB && fullness < 0.15 && !fillingFast && lean < capacity) {
    const how = `It holds ${formatBytes(used)} in ${formatBytes(capacity)}`;
    const text = `${how}: that is far more than it needs. It uses no extra disk today, but it counts against what the cluster can schedule.`;
    lose(fullness < 0.03 ? 50 : fullness < 0.08 ? 65 : 80, text);
  }

  // ---- Health ----
  if (volume.robustness === "faulted") lose(10, "The storage system reports this volume as faulted.");
  else if (volume.robustness === "degraded") lose(50, "The storage system reports this volume as degraded: some copies are missing.");

  result.points = points;
  result.reasons = problems.sort((a, b) => b.cost - a.cost).map((problem) => problem.text);

  // ---- The fix ----
  const tooSmall = fullness >= 0.75 || (daysToFull !== undefined && daysToFull < 30);
  if (tooSmall) {
    // Aim to be at most 80% full after 60 more days, bigger than today, and never more
    // than four times today's size in one step: a trend over a few days is a rough guess,
    // and a volume can be grown again later but never shrunk.
    const projected60 = used + Math.max(growth ?? 0, 0) * 60;
    const wanted = Math.max(niceSize(Math.max(used / 0.5, projected60 / 0.8)), niceSize(capacity + 1));
    const want = Math.min(wanted, Math.max(niceSize(capacity * 4), niceSize(capacity + 1)));
    result.suggestion = {
      kind: "grow",
      newBytes: want,
      why: `${formatBytes(want)} leaves room for growth for a while. It can be made bigger in place, and cannot be made smaller afterwards${want < wanted ? "; if it keeps filling at this pace, grow it again later" : ""}.`,
    };
  } else if (capacity >= 2 * GIB && fullness < 0.15 && !fillingFast && lean < capacity) {
    result.suggestion = {
      kind: "shrink",
      newBytes: lean,
      why: `${formatBytes(lean)} would still leave room for growth. A volume cannot be shrunk in place: it needs a new, smaller volume and a copy of the data.`,
    };
  }
  return result;
}

export interface StorageSizing {
  // 0..100, or null when no volume could be measured.
  points: number | null;
  volumes: VolumeSizing[];
  reasons: string[];
  // Volumes that exist but could not be measured.
  unmeasured: string[];
}

// One score for an app's volumes. The worst volume counts for most: a full volume is an
// outage however tidy the others are.
export function sizeStorage(volumes: Volume[], history: Map<string, HistoryVolume> | undefined): StorageSizing {
  const sized = volumes.map((volume) => sizeVolume(volume, history?.get(`${volume.namespace}/${volume.name}`)));
  const measured = sized.filter((volume) => volume.points !== null);
  const points = measured.length
    ? Math.round(0.6 * Math.min(...measured.map((v) => v.points as number)) + 0.4 * (measured.reduce((sum, v) => sum + (v.points as number), 0) / measured.length))
    : null;
  const many = sized.length > 1;
  return {
    points,
    volumes: sized,
    reasons: measured
      .sort((a, b) => (a.points as number) - (b.points as number))
      .flatMap((volume) => volume.reasons.slice(0, 1).map((text) => (many ? `${volume.name}: ${text}` : text))),
    unmeasured: sized.filter((volume) => volume.points === null).map((volume) => volume.name),
  };
}
