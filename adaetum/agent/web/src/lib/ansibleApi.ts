import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch } from "./auth";

// Client for the agent's playbook launcher (agent/internal/ansiblerun).
//
// The agent does not push anything itself: it runs an Ansible playbook the person
// picks from a fixed catalog, with the settings they fill in. The page is the form
// and the button; the catalog, the checks, and the record live in the agent.

export type VarKind = "text" | "path" | "flag" | "choice" | "secret";

export interface PlaybookVar {
  name: string;
  label: string;
  help?: string;
  kind: VarKind;
  required?: boolean;
  default?: string;
  choices?: string[];
}

export interface PlaybookInfo {
  id: string;
  title: string;
  description: string;
  limitRequired: boolean;
  vars: PlaybookVar[];
}

export interface Capabilities {
  enabled: boolean;
  reason?: string;
  controller?: string;
  catalog: PlaybookInfo[];
}

export type RunStatus = "running" | "succeeded" | "failed" | "cancelled" | "error";

export interface Run {
  id: string;
  playbook: string;
  title: string;
  limit: string[];
  check: boolean;
  vars: Record<string, string>;
  status: RunStatus;
  startedBy: string;
  startedAt: string;
  finishedAt?: string;
  exitCode?: number;
  error?: string;
}

export interface RunView extends Run {
  lines: string[];
  firstLine: number;
  nextLine: number;
  dropped: boolean;
}

export interface LaunchRequest {
  playbook: string;
  limit: string;
  check: boolean;
  vars: Record<string, string>;
}

// The agent's error messages are written for people ("name the servers to run this
// on"), so they are shown as they are.
async function failure(response: Response, fallback: string): Promise<Error> {
  try {
    const body = await response.json();
    if (typeof body?.error === "string" && body.error) return new Error(body.error);
  } catch {
    /* not JSON */
  }
  return new Error(`${fallback} (${response.status})`);
}

export async function fetchCapabilities(signal?: AbortSignal): Promise<Capabilities> {
  const response = await authedFetch("/api/v1/ansible/capabilities", { signal });
  if (!response.ok) throw await failure(response, "Could not read what this agent can run");
  return response.json();
}

export async function listRuns(signal?: AbortSignal): Promise<Run[]> {
  const response = await authedFetch("/api/v1/ansible/runs", { signal });
  if (!response.ok) throw await failure(response, "Could not read recent launches");
  return (await response.json()).runs ?? [];
}

export async function startRun(request: LaunchRequest): Promise<string> {
  const response = await authedFetch("/api/v1/ansible/runs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  if (!response.ok) throw await failure(response, "Could not start the run");
  return (await response.json()).id;
}

export async function fetchRun(id: string, after: number, signal?: AbortSignal): Promise<RunView> {
  const response = await authedFetch(`/api/v1/ansible/runs/${encodeURIComponent(id)}?after=${after}`, { signal, quiet: true });
  if (!response.ok) throw await failure(response, "Could not read the run");
  return response.json();
}

export async function cancelRun(id: string): Promise<void> {
  const response = await authedFetch(`/api/v1/ansible/runs/${encodeURIComponent(id)}/cancel`, { method: "POST" });
  if (!response.ok) throw await failure(response, "Could not stop the run");
}

export interface CapabilitiesState {
  data: Capabilities | null;
  error: string | null;
}

export function useCapabilities(): CapabilitiesState {
  const [data, setData] = useState<Capabilities | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    fetchCapabilities(controller.signal)
      .then(setData)
      .catch((err) => {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
      });
    return () => controller.abort();
  }, []);
  return { data, error };
}

export interface RunState {
  run: RunView | null;
  error: string | null;
}

// useRun follows one run: it polls for new output while the run is going and keeps
// the lines it has already received, asking only for what is new.
export function useRun(id: string | null, intervalMs = 1000): RunState {
  const [run, setRun] = useState<RunView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const next = useRef(0);
  const lines = useRef<string[]>([]);

  const poll = useCallback(
    async (signal: AbortSignal): Promise<boolean> => {
      if (!id) return false;
      try {
        const view = await fetchRun(id, next.current, signal);
        lines.current = [...lines.current, ...view.lines];
        next.current = view.nextLine;
        setRun({ ...view, lines: lines.current });
        setError(null);
        return view.status === "running";
      } catch (err) {
        if (!signal.aborted) setError(err instanceof Error ? err.message : String(err));
        return true; // keep trying: a missed poll is not the end of the run
      }
    },
    [id],
  );

  useEffect(() => {
    next.current = 0;
    lines.current = [];
    setRun(null);
    setError(null);
    if (!id) return;

    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      const keepGoing = await poll(controller.signal);
      if (keepGoing && !controller.signal.aborted) timer = setTimeout(tick, intervalMs);
    };
    tick();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [id, intervalMs, poll]);

  return { run, error };
}
