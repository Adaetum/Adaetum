import { formatBytes, plural } from "./format";
import { CleanupPlan, PlanItem, Run, Schedule } from "./cleanupApi";

// The runtime reports compressed sizes, which understate the disk an image
// frees. When the agent has measured the real ratio on this server it is passed
// in; this default is the typical one seen on the production servers.
export const DEFAULT_DISK_PER_COMPRESSED = 2.5;

export function estimateDisk(compressedBytes: number, ratio: number): string {
  return `about ${formatBytes(Math.round(compressedBytes * ratio))}`;
}

export const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export function ordinal(day: number): string {
  const suffix = day % 100 >= 11 && day % 100 <= 13 ? "th" : ({ 1: "st", 2: "nd", 3: "rd" } as Record<number, string>)[day % 10] ?? "th";
  return `${day}${suffix}`;
}

// describeSchedule turns a schedule into the sentence shown next to it.
export function describeSchedule(schedule: Schedule): string {
  if (schedule.frequency === "off") return "No schedule";
  const when =
    schedule.frequency === "daily"
      ? `every day at ${schedule.time}`
      : schedule.frequency === "weekly"
        ? `every ${WEEKDAYS[schedule.weekday]} at ${schedule.time}`
        : `on the ${ordinal(schedule.dayOfMonth)} of each month at ${schedule.time}`;
  const condition = schedule.onlyIfDiskOverPercent > 0 ? `, only if a disk is over ${schedule.onlyIfDiskOverPercent}% full` : "";
  const manner = schedule.level === "aggressive" ? " aggressively" : " carefully";
  return `${schedule.checkOnly ? "Checks" : "Cleans"}${manner} ${when}${condition}`;
}

export function triggerLabel(trigger: string): string {
  return trigger === "schedule" ? "Scheduled" : trigger === "ui" ? "From this page" : trigger === "cli" ? "Command line" : "Manual";
}

// describeRun gives one honest line for a past run. A dry run never claims a removal.
export function describeRun(run: Run): string {
  if (!run.complete) return "Did not finish (the agent may have stopped)";
  const size = run.removedCompressedBytes > 0 ? `, ${formatBytes(run.removedCompressedBytes)} as the runtime counts them` : "";
  const problems = [run.failed > 0 ? `${run.failed} failed` : "", run.skipped > 0 ? `${run.skipped} skipped` : ""].filter(Boolean).join(", ");
  const tail = problems ? ` (${problems})` : "";
  if (run.dryRun) return `Checked only: ${plural(run.removed, "image")} would be removed${tail}`;
  return `Removed ${plural(run.removed, "image")}${size}${tail}`;
}

export interface RepoGroup {
  repository: string;
  count: number;
  bytes: number;
  category: PlanItem["category"];
}

// categoryLabel says, in plain words, why an image is on the removal list.
export const CATEGORY_LABEL: Record<PlanItem["category"], { label: string; tone: "gray" | "amber" }> = {
  superseded: { label: "Older version", tone: "gray" },
  orphan: { label: "App not running", tone: "amber" },
  referenced: { label: "Another server or a rollback may want it", tone: "amber" },
  unnamed: { label: "Unnamed", tone: "gray" },
};

// groupItems groups planned images by app (repository), largest first.
export function groupItems(plan: CleanupPlan): RepoGroup[] {
  const groups = new Map<string, RepoGroup>();
  for (const item of plan.items) {
    const group = groups.get(item.repository) ?? { repository: item.repository, count: 0, bytes: 0, category: item.category };
    group.count += 1;
    group.bytes += item.compressedBytes;
    groups.set(item.repository, group);
  }
  return [...groups.values()].sort((a, b) => b.bytes - a.bytes);
}

export interface KeptGroup {
  reason: string;
  // Plain wording for the reason, which the agent words for operators.
  label: string;
  count: number;
  bytes: number;
}

const BLOCKED_REGISTRY = /^from (\S+), which is not on the allowed list/;

// registryOf extracts the registry from a "not on the allowed list" reason.
export function blockedRegistry(reason: string): string | undefined {
  return BLOCKED_REGISTRY.exec(reason)?.[1];
}

// groupKept summarizes why images are kept. Every "not on the allowed list"
// reason names a different registry, so those are merged into one line.
export function groupKept(plan: CleanupPlan): KeptGroup[] {
  const groups = new Map<string, KeptGroup>();
  for (const excluded of plan.excluded) {
    const blocked = blockedRegistry(excluded.reason) !== undefined;
    const key = blocked ? "blocked-registry" : excluded.reason;
    const label = blocked
      ? "From registries that are not known to be public. Removing them could leave you unable to download them again."
      : excluded.reason.charAt(0).toUpperCase() + excluded.reason.slice(1);
    const group = groups.get(key) ?? { reason: key, label, count: 0, bytes: 0 };
    group.count += 1;
    group.bytes += excluded.compressedBytes;
    groups.set(key, group);
  }
  return [...groups.values()].sort((a, b) => b.bytes - a.bytes);
}

// blockedRegistries lists the registries whose images the plan kept, with sizes,
// so the operator can choose to allow one.
export function blockedRegistries(plan: CleanupPlan): { registry: string; count: number; bytes: number }[] {
  const byRegistry = new Map<string, { registry: string; count: number; bytes: number }>();
  for (const excluded of plan.excluded) {
    const registry = blockedRegistry(excluded.reason);
    if (!registry) continue;
    const entry = byRegistry.get(registry) ?? { registry, count: 0, bytes: 0 };
    entry.count += 1;
    entry.bytes += excluded.compressedBytes;
    byRegistry.set(registry, entry);
  }
  return [...byRegistry.values()].sort((a, b) => b.bytes - a.bytes);
}
