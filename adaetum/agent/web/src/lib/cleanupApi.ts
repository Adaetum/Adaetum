// Client for the agent's image-cleanup API (agent/internal/cleanup/api.go).
// Previews and reads need no token; anything that changes the server does, and
// the agent refuses it outright when it cannot make changes.

import { trackBusy } from "./busy";
import { tokenStore } from "./auth";

export interface Capabilities {
  host: string;
  canApply: boolean;
  reason?: string;
  hint?: string;
  defaultRegistries: string[];
}

export type Level = "aggressive" | "careful";

export interface PlanItem {
  id: string;
  names: string[];
  repository: string;
  registry: string;
  category: "superseded" | "orphan" | "referenced" | "unnamed";
  compressedBytes: number;
}

export interface PlanExclusion {
  id: string;
  name: string;
  reason: string;
  compressedBytes: number;
}

export interface CleanupPlan {
  id: string;
  host: string;
  createdAt: string;
  expiresAt: string;
  items: PlanItem[];
  excluded: PlanExclusion[];
  // Things the operator should know about this plan.
  notes?: string[];
}

export interface PlanRequest {
  level: Level;
  includeOrphans: boolean;
  allowRegistries: string[];
}

export interface AuditEntry {
  time: string;
  event: string;
  image?: string;
  names?: string[];
  reason?: string;
  compressedBytes?: number;
}

export interface Job {
  id: string;
  trigger: string;
  dryRun: boolean;
  state: "running" | "done" | "failed";
  startedAt: string;
  endedAt?: string;
  entries: AuditEntry[];
  total: number;
  error?: string;
  outcome?: {
    dryRun: boolean;
    removed: number;
    skipped: number;
    failed: number;
    removedCompressedBytes: number;
    stopped?: string;
  };
}

export interface Run {
  planId: string;
  trigger: string;
  dryRun: boolean;
  startedAt: string;
  endedAt?: string;
  removed: number;
  skipped: number;
  failed: number;
  removedCompressedBytes: number;
  summary?: string;
  complete: boolean;
}

export type Frequency = "off" | "daily" | "weekly" | "monthly";

export interface Schedule {
  level: Level;
  frequency: Frequency;
  weekday: number;
  dayOfMonth: number;
  time: string;
  onlyIfDiskOverPercent: number;
  includeOrphans: boolean;
  allowRegistries: string[];
  maxImages: number;
  checkOnly: boolean;
}

export interface ScheduleView {
  available: boolean;
  state: {
    schedule: Schedule;
    nextRun?: string;
    lastRun?: { at: string; status: string; detail: string; jobId?: string };
  };
}

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}

async function call<T>(
  path: string,
  options: { method?: string; body?: unknown; token?: string; signal?: AbortSignal; quiet?: boolean } = {},
): Promise<T> {
  const headers: Record<string, string> = {};
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  const token = options.token || tokenStore.get();
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const sent = fetch(`/api/v1/images${path}`, {
    method: options.method ?? "GET",
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  });
  const response = await (options.quiet ? sent : trackBusy(sent));
  const text = await response.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* a non-JSON body (for example a proxy error page) is reported below */
  }
  if (!response.ok) {
    const message = (data as { error?: string } | null)?.error ?? `The agent returned an error (${response.status}).`;
    throw new ApiError(message, response.status);
  }
  return data as T;
}

export const getCapabilities = (signal?: AbortSignal) => call<Capabilities>("/capabilities", { signal });
export const makePlan = (request: PlanRequest, signal?: AbortSignal) => call<CleanupPlan>("/plan", { method: "POST", body: request, signal });
export const startApply = (planId: string, maxImages: number, dryRun: boolean, token: string) =>
  call<{ jobId: string }>("/apply", { method: "POST", body: { planId, maxImages, dryRun }, token });
export const getJob = (id: string, signal?: AbortSignal) => call<Job>(`/jobs/${encodeURIComponent(id)}`, { signal, quiet: true });
export const cancelJob = (token: string) => call<{ ok: boolean }>("/cancel", { method: "POST", body: {}, token });
export const getHistory = (signal?: AbortSignal) => call<Run[]>("/history", { signal });
export const getSchedule = (signal?: AbortSignal) => call<ScheduleView>("/schedule", { signal });
export const saveSchedule = (schedule: Schedule, token: string) => call<ScheduleView>("/schedule", { method: "PUT", body: schedule, token });

// The token store is shared by the whole UI; see auth.ts.
export { tokenStore };
