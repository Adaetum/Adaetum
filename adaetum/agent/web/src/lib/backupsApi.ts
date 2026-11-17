import { useCallback, useEffect, useState } from "react";
import { authedFetch } from "./auth";

// Client for Settings > Backups (agent/internal/backups): what Longhorn backs up, read-only.

export interface BackupTarget {
  configured: boolean;
  available: boolean;
  url?: string;
  message?: string;
}

export interface BackupJob {
  name: string;
  task: string;
  cron: string;
  retain: number;
  groups?: string[];
}

export interface BackupVolume {
  name: string;
  namespace?: string;
  claim?: string;
  // Maximum (provisioned) size, and what it holds now, which is what a backup copies.
  sizeBytes: number;
  usedBytes: number;
  backup?: string;
  snapshot?: string;
  lastBackupAt?: string;
  // Chosen on this page for scheduled backups.
  // The named schedule it is on, if any.
  schedule?: string;
  sync: boolean;
  lastSyncAt?: string;
  // "both" means a change between modes is still waiting for a pull request to be merged.
  mode: "off" | "scheduled" | "sync" | "both";
  state: "protected" | "stale" | "unprotected";
}

export interface Schedule {
  name: string;
  cron: string;
  retain: number;
  volumes?: number;
}

export interface StorageInfo {
  configured: boolean;
  provider?: string;
  bucket?: string;
}

export interface BackupsState {
  schedules: Schedule[];
  // Whether VolSync's own pod runs: how many of its replicas are wanted and ready.
  sync: { desired: number; ready: number };
  // Where each tier copies to, never the credentials.
  storage?: Record<"scheduled" | "sync", StorageInfo>;
  target: BackupTarget;
  jobs: BackupJob[];
  volumes: BackupVolume[];
  syncInstalled: boolean;
  summary: "off" | "failing" | "attention" | "unprotected" | "ok";
}

// null means this agent cannot read Longhorn (a host agent, or no cluster).
export function useBackups(intervalMs = 30_000) {
  const [state, setState] = useState<BackupsState | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const refresh = useCallback(() => {
    authedFetch("/api/v1/backups", { quiet: true }).then(async (response) => {
      if (response.status === 404) return setState(null);
      if (!response.ok) {
        let message = `Could not read the backup state (${response.status})`;
        try {
          const body = await response.json();
          if (typeof body?.error === "string" && body.error) message = body.error;
        } catch {
          /* not JSON */
        }
        throw new Error(message);
      }
      setState(await response.json());
      setError(null);
    }).catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, []);
  useEffect(() => {
    refresh();
    const timer = window.setInterval(refresh, intervalMs);
    return () => window.clearInterval(timer);
  }, [refresh, intervalMs]);
  return { state, error, refresh };
}

export interface CloudflareAccount {
  id: string;
  name: string;
}

export interface StorageRequest {
  tier: "scheduled" | "sync";
  // Sync only: reuse the scheduled backups' storage.
  same?: boolean;
  provider?: "r2" | "s3" | "azure";
  // R2: a Cloudflare token, used once and not kept.
  token?: string;
  accountId?: string;
  // S3-compatible.
  endpoint?: string;
  region?: string;
  bucket?: string;
  accessKeyId?: string;
  secretAccessKey?: string;
  // Azure.
  account?: string;
  container?: string;
  accountKey?: string;
}

export interface StorageResult {
  bucket: string;
  proposalUrl?: string;
}

// Thrown when the token can see several Cloudflare accounts and one must be chosen.
export class AccountChoice extends Error {
  constructor(public accounts: CloudflareAccount[], message: string) {
    super(message);
  }
}

async function failure(response: Response, fallback: string): Promise<never> {
  let body: { error?: string; accounts?: CloudflareAccount[] } = {};
  try {
    body = await response.json();
  } catch {
    /* not JSON */
  }
  if (response.status === 409 && body.accounts) throw new AccountChoice(body.accounts, body.error ?? "Choose an account.");
  throw new Error(body.error ?? `${fallback} (${response.status})`);
}

// Chooses where one tier copies to. The agent keeps the credentials in OpenBao; none of them is kept here.
export async function chooseStorage(request: StorageRequest): Promise<StorageResult> {
  const response = await authedFetch("/api/v1/backups/storage", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(request), quiet: true });
  if (!response.ok) return failure(response, "Could not set the storage");
  return response.json();
}

// Replaces the list of schedules, as a pull request.
export async function saveSchedules(schedules: Schedule[]): Promise<{ proposalUrl: string }> {
  const response = await authedFetch("/api/v1/backups/schedules", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ schedules: schedules.map(({ name, cron, retain }) => ({ name, cron, retain })) }), quiet: true });
  if (!response.ok) return failure(response, "Could not save the schedules");
  return response.json();
}

// Chooses whether a volume is backed up on the schedule. It labels the Longhorn volume, so it
// takes effect at once.
export async function setVolumeMode(volume: string, mode: string): Promise<{ proposalUrl?: string }> {
  const response = await authedFetch(`/api/v1/backups/volumes/${encodeURIComponent(volume)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mode }),
    quiet: true,
  });
  if (!response.ok) {
    let message = `Could not change the volume (${response.status})`;
    try {
      const body = await response.json();
      if (typeof body?.error === "string" && body.error) message = body.error;
    } catch {
      /* not JSON */
    }
    throw new Error(message);
  }
  return response.json();
}

export interface DefaultRule {
  // "off" or "schedule:<name>".
  mode: string;
  since: string;
}

export async function getDefaultRule(): Promise<DefaultRule | null> {
  const response = await authedFetch("/api/v1/backups/default", { quiet: true });
  return response.ok ? response.json() : null;
}

// Sets what happens to volumes created from now on.
export async function setDefaultRule(mode: string): Promise<DefaultRule> {
  const response = await authedFetch("/api/v1/backups/default", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ mode }),
    quiet: true,
  });
  if (!response.ok) throw new Error(`Could not save the rule (${response.status})`);
  return response.json();
}
