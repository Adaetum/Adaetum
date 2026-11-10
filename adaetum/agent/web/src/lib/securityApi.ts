import { useCallback, useEffect, useState } from "react";
import { authedFetch } from "./auth";

// Client for the agent's security switchboard (agent/internal/policy).

export interface Lever {
  key: string;
  title: string;
  description: string;
  risk: string;
  // Whether this agent lets the page flip it. A host agent's switches are fixed
  // when it starts.
  toggleable: boolean;
  // The prerequisites hold (a token, the program it needs, a writable record).
  available: boolean;
  // Usable right now: available and switched on.
  enabled: boolean;
  reason?: string;
  until?: string;
  changedBy?: string;
  changedAt?: string;
  fromStartup: boolean;
}

async function failure(response: Response, fallback: string): Promise<Error> {
  try {
    const body = await response.json();
    if (typeof body?.error === "string" && body.error) return new Error(body.error);
  } catch {
    /* not JSON */
  }
  return new Error(`${fallback} (${response.status})`);
}

export async function fetchLevers(signal?: AbortSignal): Promise<Lever[]> {
  const response = await authedFetch("/api/v1/security", { signal, quiet: true });
  if (!response.ok) throw await failure(response, "Could not read the security settings");
  return (await response.json()).levers ?? [];
}

export async function setLever(key: string, enabled: boolean, durationMinutes: number, confirm: string): Promise<Lever> {
  const response = await authedFetch(`/api/v1/security/${encodeURIComponent(key)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled, durationMinutes, confirm }),
  });
  if (!response.ok) throw await failure(response, "Could not change the setting");
  return response.json();
}

export interface LeversState {
  levers: Lever[] | null;
  error: string | null;
  refresh: () => void;
}

// useLevers polls, because a time-boxed switch turns itself off and the page should
// notice without being reloaded.
export function useLevers(intervalMs = 15_000): LeversState {
  const [levers, setLevers] = useState<Lever[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const refresh = useCallback(() => setTick((n) => n + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    const load = () =>
      fetchLevers(controller.signal)
        .then((next) => { setLevers(next); setError(null); })
        .catch((err) => {
          if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
        });
    load();
    const timer = setInterval(load, intervalMs);
    return () => { controller.abort(); clearInterval(timer); };
  }, [intervalMs, tick]);

  return { levers, error, refresh };
}
