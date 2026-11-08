import { useCallback, useEffect, useRef, useState } from "react";
import { fetchReport, Report } from "../api";
import { AuthRequiredError } from "../lib/auth";

export interface ReportState {
  report: Report | null;
  error: string | null;
  refreshing: boolean;
  updatedAt: number | null;
  // The agent wants an access token, or did not accept the one sent.
  authRequired: boolean;
  refresh: () => void;
}

// useReport polls the agent. A failed refresh keeps the last good report so the
// UI can show it as stale instead of going blank.
export function useReport(intervalMs = 30_000): ReportState {
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const [authRequired, setAuthRequired] = useState(false);
  const inflight = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    // Collection can take seconds; never stack requests on a slow host.
    if (inflight.current) return;
    const controller = new AbortController();
    inflight.current = controller;
    setRefreshing(true);
    try {
      setReport(await fetchReport(controller.signal));
      setError(null);
      setAuthRequired(false);
      setUpdatedAt(Date.now());
    } catch (err) {
      if (controller.signal.aborted) {
        /* superseded; nothing to report */
      } else if (err instanceof AuthRequiredError) {
        // Not a failure: the agent is up and asking who is there.
        setAuthRequired(true);
        setError(null);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      // Only clear our own slot: React StrictMode starts a second request after
      // aborting the first, and the first must not clobber it.
      if (inflight.current === controller) inflight.current = null;
      setRefreshing(false);
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

  return { report, error, refreshing, updatedAt, authRequired, refresh };
}

// useNow re-renders on an interval so relative times ("12s ago") stay current.
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}
