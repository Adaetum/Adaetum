import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, AuthRequiredError } from "./auth";

// What the agent says about itself and, on a host, about the cluster agent (the pod).
// Mirrors cmd/adaetum-agent/connection.go.
export interface Connection {
  role: "host" | "cluster";
  // Host only. "none" means this host has not joined a cluster, which is normal.
  cluster?: "reachable" | "unreachable" | "none";
}

export interface ConnectionState {
  connection: Connection | null;
  // The agent itself did not answer: the page cannot reach its back end at all.
  offline: boolean;
  // When the last answer arrived.
  checkedAt: number | null;
  refresh: () => void;
}

export function useConnection(intervalMs = 30_000): ConnectionState {
  const [connection, setConnection] = useState<Connection | null>(null);
  const [offline, setOffline] = useState(false);
  const [checkedAt, setCheckedAt] = useState<number | null>(null);
  const inflight = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    if (inflight.current) return;
    const controller = new AbortController();
    inflight.current = controller;
    try {
      const response = await authedFetch("/api/v1/connection", { signal: controller.signal });
      // An older agent has no such route: it answered, so it is not offline.
      if (response.ok) setConnection(await response.json());
      setOffline(false);
      setCheckedAt(Date.now());
    } catch (err) {
      if (controller.signal.aborted) return;
      // Being asked to sign in means the agent is up.
      if (err instanceof AuthRequiredError) setOffline(false);
      else setOffline(true);
    } finally {
      if (inflight.current === controller) inflight.current = null;
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

  return { connection, offline, checkedAt, refresh };
}
