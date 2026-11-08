import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch } from "./auth";

// These types mirror internal/automation (Overview and the types it contains).

export type RunStatus = "success" | "failed" | "unreachable" | "incomplete";

export interface HostResult {
  host: string;
  ok: number;
  changed: number;
  unreachable: number;
  failed: number;
  skipped: number;
}

export interface Run {
  startedAt: string;
  playbook: string;
  status: RunStatus;
  hosts: HostResult[];
  changed: number;
  error?: string;
}

export interface Health {
  total: number;
  succeeded: number;
  failed: number;
  consecutiveFailures: number;
  failingSince?: string;
  lastSuccess?: string;
  lastError?: string;
}

export interface RunnerPod {
  name: string;
  phase: string;
  node: string;
  startedAt?: string;
  restarts: number;
}

export interface Runner {
  found: boolean;
  image?: string;
  pullPolicy?: string;
  schedule?: string;
  playbook?: string;
  inventory?: string;
  replicas: number;
  ready: number;
  pod?: RunnerPod;
}

export interface Playbook {
  file: string;
  description?: string;
  plays?: string[];
}

export interface Declared {
  hostMaintenance?: { enabled: boolean; updatePolicy: string; rebootWindow: string };
  imageCleanup?: {
    enabled: boolean;
    level: string;
    time: string;
    maxImagesPerRun: number;
    onlyIfDiskOverPercent: number;
    checkOnly: boolean;
  };
  imageSharing?: { enabled: boolean; registries: string[] };
}

export interface AutomationOverview {
  collectedAt: string;
  runner: Runner;
  health: Health;
  runs: Run[];
  playbooks: Playbook[];
  declared?: Declared;
  errors?: string[];
}

export async function fetchAutomation(signal?: AbortSignal): Promise<AutomationOverview> {
  const response = await authedFetch("/api/v1/automation", { signal });
  if (!response.ok) throw new Error(`The agent answered ${response.status}`);
  return response.json();
}

export interface AutomationState {
  data: AutomationOverview | null;
  error: string | null;
  loading: boolean;
  refresh: () => void;
}

// useAutomation loads on mount and refreshes on a slow interval. Reading the
// runner means running commands inside its pod, so it is polled far less often
// than the main report, and only while the Automation page is open.
export function useAutomation(intervalMs = 60_000): AutomationState {
  const [data, setData] = useState<AutomationOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const inflight = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    if (inflight.current) return;
    const controller = new AbortController();
    inflight.current = controller;
    setLoading(true);
    try {
      setData(await fetchAutomation(controller.signal));
      setError(null);
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (inflight.current === controller) inflight.current = null;
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, intervalMs);
    return () => {
      clearInterval(timer);
      inflight.current?.abort();
      inflight.current = null;
    };
  }, [refresh, intervalMs]);

  return { data, error, loading, refresh };
}
