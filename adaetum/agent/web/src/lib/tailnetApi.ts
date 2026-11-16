import { useCallback, useEffect, useState } from "react";
import { authedFetch } from "./auth";

// Client for the cluster agent's Tailscale HTTPS switch (agent/internal/tailnet).

export interface TailnetHttps {
  // "on": every Tailscale link has an address. "off": some are still waiting for one, which
  // is what a tailnet without HTTPS certificates looks like. "unknown": nothing to judge by.
  state: "on" | "off" | "unknown";
  waiting: number;
  total: number;
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

export async function fetchTailnetHttps(signal?: AbortSignal): Promise<TailnetHttps | null> {
  const response = await authedFetch("/api/v1/tailnet/https", { signal, quiet: true });
  if (response.status === 404) return null; // a host agent: no cluster, no switch
  if (!response.ok) throw await failure(response, "Could not read the Tailscale HTTPS state");
  return response.json();
}

// The token is sent once, in this request, and not kept by the page or the agent.
export async function setTailnetHttps(enabled: boolean, token: string): Promise<TailnetHttps> {
  const response = await authedFetch("/api/v1/tailnet/https", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled, token }),
  });
  if (!response.ok) throw await failure(response, "Could not change the setting");
  return response.json();
}

// Polls, because after a change the operator needs a while to hand out the addresses.
export function useTailnetHttps(intervalMs = 10_000) {
  const [value, setValue] = useState<TailnetHttps | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const refresh = useCallback(() => {
    fetchTailnetHttps().then((next) => { setValue(next); setError(null); }, (err) => setError(err instanceof Error ? err.message : String(err)));
  }, []);
  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, intervalMs);
    return () => clearInterval(timer);
  }, [refresh, intervalMs]);
  return { value, error, refresh };
}
