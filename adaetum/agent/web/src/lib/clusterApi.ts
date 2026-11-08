import { useCallback, useEffect, useRef, useState } from "react";
import { AuthRequiredError, authedFetch } from "./auth";

// Mirrors the cluster agent's cluster view (internal/cluster/api.go, internal/membership).

export type ServerState = "ok" | "missing" | "stale" | "outdated";

export interface ServerStatus {
  server: string;
  status: ServerState;
  hostId?: string;
  version?: string;
  lastSeen?: string;
}

export interface UnmatchedCheckIn {
  hostId: string;
  hostname: string;
  version: string;
  at: string;
}

export interface HostSummary {
  hostId: string;
  hostname: string;
  status: "approved" | "pending";
  reason: string;
  enrolledAt: string;
  approvedBy?: string;
  lastCheckIn?: string;
  version?: string;
}

export interface InstallAction {
  server: string;
  kind: "install" | "upgrade";
  reason: string;
}

export interface InstallPlan {
  actions: InstallAction[];
  // The servers to hand to ansible-playbook --limit.
  limit: string;
  skipped?: { server: string; reason: string }[];
}

export interface ClusterView {
  servers: ServerStatus[];
  unmatched: UnmatchedCheckIn[];
  hosts: HostSummary[];
  pending: HostSummary[];
  install: InstallPlan;
  nodesError?: string;
}

// NotClusterAgentError means the agent answered 404: it is a host agent, which has
// no cluster view. That is the usual state of a host, not a failure.
export class NotClusterAgentError extends Error {
  constructor() {
    super("This agent is not a cluster agent.");
  }
}

export async function fetchCluster(signal?: AbortSignal): Promise<ClusterView> {
  const response = await authedFetch("/api/v1/cluster", { signal });
  if (response.status === 404) throw new NotClusterAgentError();
  if (!response.ok) throw new Error(`The agent answered ${response.status}`);
  return response.json();
}

export async function approveHost(hostId: string): Promise<void> {
  const response = await authedFetch(`/api/v1/hosts/${encodeURIComponent(hostId)}/approve`, { method: "POST" });
  if (!response.ok) throw new Error(`Could not approve the host (${response.status}).`);
}

export interface JoinToken {
  secret: string;
  expiresAt: string;
}

export async function mintJoinToken(note: string, ttlMinutes: number): Promise<JoinToken> {
  const response = await authedFetch("/api/v1/join-tokens", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ note, ttlMinutes }),
  });
  if (!response.ok) throw new Error(`Could not make a join token (${response.status}).`);
  return response.json();
}

export interface ClusterState {
  data: ClusterView | null;
  error: string | null;
  // The agent is a host agent: no cluster view exists here.
  notClusterAgent: boolean;
  // The cluster agent wants an access token, or did not accept the one sent. The
  // host report is not available there, so this page has to ask for it itself.
  authRequired: boolean;
  loading: boolean;
  refresh: () => void;
}

export function useCluster(intervalMs = 30_000): ClusterState {
  const [data, setData] = useState<ClusterView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notClusterAgent, setNotClusterAgent] = useState(false);
  const [authRequired, setAuthRequired] = useState(false);
  const [loading, setLoading] = useState(false);
  const inflight = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    if (inflight.current) return;
    const controller = new AbortController();
    inflight.current = controller;
    setLoading(true);
    try {
      setData(await fetchCluster(controller.signal));
      setError(null);
      setNotClusterAgent(false);
      setAuthRequired(false);
    } catch (err) {
      if (controller.signal.aborted) {
        /* superseded */
      } else if (err instanceof AuthRequiredError) {
        setAuthRequired(true);
        setError(null);
      } else if (err instanceof NotClusterAgentError) {
        setNotClusterAgent(true);
        setError(null);
      } else {
        // A 401 propagates to the app's sign-in handling through the report; here
        // it is shown like any other failure.
        setError(err instanceof Error ? err.message : String(err));
      }
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

  return { data, error, notClusterAgent, authRequired, loading, refresh };
}
