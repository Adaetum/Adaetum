import { useCallback, useEffect, useState } from "react";
import { fetchReport } from "../api";
import { authedFetch } from "./auth";

// Client for Settings > Notifications (agent/internal/notify). Destination addresses usually
// contain a token, so the agent never sends them back: the page only ever sees a masked label.

export interface Destination {
  id: string;
  label: string;
  builtIn?: boolean;
  custom?: boolean;
}

export interface NotificationsState {
  destinations: Destination[];
  editable: boolean;
  reason?: string;
  lastTest?: { at: string; ok: boolean; message: string };
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

async function send(path: string, method: string, body?: unknown): Promise<NotificationsState> {
  const response = await authedFetch(path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!response.ok) throw await failure(response, "That did not work");
  return response.json();
}

export const sendTest = () => send("/api/v1/notifications/test", "POST");
export const addDestination = (url: string) => send("/api/v1/notifications/destinations", "POST", { url });
export const removeDestination = (id: string) => send(`/api/v1/notifications/destinations/${encodeURIComponent(id)}`, "DELETE");

// null means this agent has no notifications section (a host agent, or no OpenBao).
export function useNotifications() {
  const [state, setState] = useState<NotificationsState | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const [ntfyHost, setNtfyHost] = useState<string | undefined>();
  const refresh = useCallback(() => {
    authedFetch("/api/v1/notifications", { quiet: true }).then(async (response) => {
      if (response.status === 404) return setState(null);
      if (!response.ok) throw await failure(response, "Could not read the notification settings");
      setState(await response.json());
      setError(null);
    }).catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, []);
  useEffect(() => {
    refresh();
    // The cluster's own ntfy has a web address in its Ingress; the page shows it so a phone can subscribe.
    fetchReport().then((report) => {
      const hosts = (report.rke2.ingresses ?? []).filter((ingress) => ingress.name.includes("ntfy")).flatMap((ingress) => ingress.hosts);
      setNtfyHost(hosts.find((host) => !host.endsWith(".invalid")) ?? hosts[0]);
    }).catch(() => undefined);
  }, [refresh]);
  return { state, setState, error, refresh, ntfyHost };
}
