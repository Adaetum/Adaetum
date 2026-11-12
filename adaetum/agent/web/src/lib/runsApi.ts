import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch } from "./auth";
import { Tone } from "./status";

// Client for the agent's run engine (agent/internal/runs and runsapi).
//
// A *procedure* is a named list of steps the agent knows how to do (bring up a node,
// unseal OpenBao, run the doctor). A *run* is one go at a procedure. Every step
// checks first, so running again only does what is still missing: that is why Retry
// is always safe, and why this page can offer it freely.

export type RunState = "running" | "succeeded" | "failed" | "needs-human" | "cancelled";
export type StepState = "pending" | "applying" | "waiting" | "done" | "failed";
export type StepKind = "convergent" | "once-only" | "destructive";

export interface StepStatus {
  name: string;
  state: StepState;
  attempts: number;
  code?: string;
  detail?: string;
  failure?: string;
}

export interface RunSnapshot {
  id: string;
  procedure: string;
  inputs?: Record<string, string>;
  startedBy?: string;
  approved?: string[];
  attempt: number;
  state: RunState;
  steps: StepStatus[];
  started: string;
  finished?: string;
  failedStep?: string;
  failure?: string;
  code?: string;
  detail?: string;
}

// One fact about a run, as the agent journaled it.
export interface RunRecord {
  runId: string;
  kind: string;
  at: string;
  step?: string;
  attempt?: number;
  outcome?: string;
  code?: string;
  detail?: string;
  failure?: string;
  state?: RunState;
  ms?: number;
}

export interface StepInfo {
  name: string;
  needs?: string[];
  locks?: string[];
  kind: StepKind;
}

export interface ProcedureInfo {
  name: string;
  description?: string;
  steps: StepInfo[];
}

export interface RunCapabilities {
  available: boolean;
  enabled: boolean;
  reason?: string;
}

export interface RunDetail {
  run: RunSnapshot;
  events: RunRecord[];
  total: number;
}

export interface StartRequest {
  procedure: string;
  inputs: Record<string, string>;
  approved: string[];
}

// ---- wording ---------------------------------------------------------------
// The agent records reasons as short codes from a fixed list so they can be counted.
// People get sentences. Unknown codes show nothing rather than a raw code.

const REASONS: Record<string, string> = {
  already: "Already in place",
  applied: "Done",
  starting: "Starting up",
  "image-pull": "An image can't be downloaded",
  "image-pulling": "Downloading an image",
  "crash-loop": "Something keeps crashing",
  unschedulable: "There is no room to place it",
  volume: "Storage isn't ready",
  network: "Can't reach something it needs",
  auth: "A login was refused",
  config: "Something is set up wrongly",
  disk: "There isn't enough free disk space",
  sealed: "OpenBao is sealed",
  unknown: "Couldn't tell what state it is in",
  approval: "Needs your approval",
  retries: "Gave up after several tries",
  journal: "Couldn't be recorded",
};

export function reasonText(code?: string): string {
  return (code && REASONS[code]) || "";
}

const FAILURES: Record<string, string> = {
  transient: "A temporary problem",
  broken: "Something needs fixing",
  "needs-human": "Waiting for you",
  stalled: "It stopped making progress",
  timeout: "It took too long",
  cancelled: "Stopped",
  journal: "It couldn't be recorded",
};

export function failureText(failure?: string): string {
  return (failure && FAILURES[failure]) || "";
}

export const RUN_STATE: Record<RunState, { label: string; tone: Tone }> = {
  running: { label: "Running", tone: "blue" },
  succeeded: { label: "Done", tone: "green" },
  failed: { label: "Failed", tone: "red" },
  "needs-human": { label: "Needs you", tone: "amber" },
  cancelled: { label: "Stopped", tone: "gray" },
};

export const STEP_STATE: Record<StepState, { label: string; tone: Tone }> = {
  pending: { label: "Waiting its turn", tone: "gray" },
  applying: { label: "Working", tone: "blue" },
  waiting: { label: "Waiting", tone: "blue" },
  done: { label: "Done", tone: "green" },
  failed: { label: "Failed", tone: "red" },
};

export function stepsDone(run: RunSnapshot): number {
  return run.steps.filter((step) => step.state === "done").length;
}

// A step the engine stopped on purpose, for a person to decide, is not a failure: say
// "Needs you" rather than "Failed".
export function stepDisplay(step: StepStatus): { label: string; tone: Tone } {
  if (step.state === "failed" && step.failure === "needs-human") return { label: "Needs you", tone: "amber" };
  return STEP_STATE[step.state];
}

// What to show under a step, in one line. A failed step says what kind of failure it
// was and why; a step still in progress says what it is doing. The longer sentence
// the agent recorded is shown once, in the banner, not repeated here.
export function stepNote(step: StepStatus): string {
  if (step.state === "failed") return [failureText(step.failure), reasonText(step.code)].filter(Boolean).join(" · ");
  return [reasonText(step.code), step.detail].filter(Boolean).join(" · ");
}

// The reason a run stopped, in one sentence: where, and the agent's own explanation.
export function stopReason(run: RunSnapshot): string {
  if (run.state === "succeeded" || run.state === "running") return "";
  if (run.state === "cancelled") return "This run was stopped.";
  const where = run.failedStep ? `At “${run.failedStep}”: ` : "";
  const what = run.detail || reasonText(run.code) || failureText(run.failure) || "It stopped.";
  return `${where}${what}`;
}

// One line per journal record, for the event list.
export function describeRecord(rec: RunRecord): string {
  const step = rec.step ? `“${rec.step}”` : "The run";
  switch (rec.kind) {
    case "run.started":
      return "Run started";
    case "run.resumed":
      return rec.detail === "retry" ? "Tried again" : "Picked up again after the agent restarted";
    case "run.finished":
      return rec.state === "succeeded" ? "Run finished" : `Run ended: ${RUN_STATE[rec.state ?? "failed"].label.toLowerCase()}`;
    case "step.checked":
      return rec.outcome === "satisfied" ? `${step}: already in place` : rec.outcome === "unknown" ? `${step}: couldn't tell its state` : `${step}: not done yet`;
    case "step.applying":
      return `${step}: starting`;
    case "step.applied":
      return `${step}: started, waiting for it to be ready`;
    case "step.waiting":
      return `${step}: ${rec.detail || reasonText(rec.code) || "waiting"}`;
    case "step.retry":
      return `${step}: ${reasonText(rec.code) || "a temporary problem"}, trying again${rec.ms ? ` in ${Math.round(rec.ms / 1000)}s` : ""}`;
    case "step.done":
      return rec.code === "already" ? `${step}: already in place` : `${step}: done`;
    case "step.failed":
      return `${step}: ${[failureText(rec.failure), reasonText(rec.code), rec.detail].filter(Boolean).join(" · ")}`;
    default:
      return rec.kind;
  }
}

// parseInputs reads the "name=value" lines from the start form. It returns an error
// naming the first bad line so the person can fix it.
export function parseInputs(text: string): { inputs: Record<string, string>; error?: string } {
  const inputs: Record<string, string> = {};
  for (const [index, raw] of text.split("\n").entries()) {
    const line = raw.trim();
    if (!line) continue;
    const at = line.indexOf("=");
    if (at <= 0) return { inputs, error: `Line ${index + 1} should look like name=value.` };
    inputs[line.slice(0, at).trim()] = line.slice(at + 1).trim();
  }
  return { inputs };
}

// ---- requests --------------------------------------------------------------

// The agent's error messages are written for people, so they are shown as they are.
async function failure(response: Response, fallback: string): Promise<Error> {
  try {
    const body = await response.json();
    if (typeof body?.error === "string" && body.error) return new Error(body.error);
  } catch {
    /* not JSON */
  }
  return new Error(`${fallback} (${response.status})`);
}

export async function fetchRunCapabilities(signal?: AbortSignal): Promise<RunCapabilities> {
  const response = await authedFetch("/api/v1/runs/capabilities", { signal, quiet: true });
  if (!response.ok) throw await failure(response, "Could not read whether runs are available");
  return response.json();
}

export async function fetchProcedures(signal?: AbortSignal): Promise<ProcedureInfo[]> {
  const response = await authedFetch("/api/v1/runs/procedures", { signal, quiet: true });
  if (!response.ok) throw await failure(response, "Could not read the procedures");
  return (await response.json()).procedures ?? [];
}

export async function fetchRuns(signal?: AbortSignal): Promise<RunSnapshot[]> {
  const response = await authedFetch("/api/v1/runs", { signal, quiet: true });
  if (!response.ok) throw await failure(response, "Could not read the runs");
  return (await response.json()).runs ?? [];
}

export async function fetchRunDetail(id: string, after: number, signal?: AbortSignal): Promise<RunDetail> {
  const response = await authedFetch(`/api/v1/runs/${encodeURIComponent(id)}?after=${after}`, { signal, quiet: true });
  if (!response.ok) throw await failure(response, "Could not read the run");
  return response.json();
}

export async function startProcedure(request: StartRequest): Promise<string> {
  const response = await authedFetch("/api/v1/runs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  if (!response.ok) throw await failure(response, "Could not start the run");
  return (await response.json()).id;
}

export async function retryRun(id: string, approve: string[]): Promise<void> {
  const response = await authedFetch(`/api/v1/runs/${encodeURIComponent(id)}/retry`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ approve }),
  });
  if (!response.ok) throw await failure(response, "Could not try again");
}

export async function cancelRun(id: string): Promise<void> {
  const response = await authedFetch(`/api/v1/runs/${encodeURIComponent(id)}/cancel`, { method: "POST" });
  if (!response.ok) throw await failure(response, "Could not stop the run");
}

// ---- hooks -----------------------------------------------------------------

export interface RunsOverview {
  capabilities: RunCapabilities | null;
  procedures: ProcedureInfo[];
  runs: RunSnapshot[];
  error: string | null;
  loading: boolean;
  refresh: () => void;
}

// useRunsOverview keeps the list fresh: every couple of seconds while something is
// running (so progress is visible), and slowly otherwise.
export function useRunsOverview(): RunsOverview {
  const [capabilities, setCapabilities] = useState<RunCapabilities | null>(null);
  const [procedures, setProcedures] = useState<ProcedureInfo[]>([]);
  const [runs, setRuns] = useState<RunSnapshot[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const anyRunning = useRef(false);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const load = async () => {
      try {
        const [caps, procs, list] = await Promise.all([
          fetchRunCapabilities(controller.signal),
          fetchProcedures(controller.signal),
          fetchRuns(controller.signal),
        ]);
        if (controller.signal.aborted) return;
        setCapabilities(caps);
        setProcedures(procs);
        // Newest first reads best for "what just happened".
        setRuns([...list].sort((a, b) => b.started.localeCompare(a.started)));
        anyRunning.current = list.some((run) => run.state === "running");
        setError(null);
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          timer = setTimeout(load, anyRunning.current ? 2_000 : 10_000);
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [tick]);

  const refresh = useCallback(() => setTick((n) => n + 1), []);
  return { capabilities, procedures, runs, error, loading, refresh };
}

export interface RunDetailState {
  detail: RunDetail | null;
  events: RunRecord[];
  error: string | null;
}

// useRunDetail follows one run, keeping the events it already has and asking only
// for new ones. It slows down once the run is over but keeps going, so a retry
// started elsewhere shows up here too.
export function useRunDetail(id: string | null, nudge: number): RunDetailState {
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [events, setEvents] = useState<RunRecord[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setDetail(null);
    setEvents([]);
    setError(null);
    if (!id) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let have: RunRecord[] = [];
    const tick = async () => {
      let delay = 1_500;
      try {
        const view = await fetchRunDetail(id, have.length, controller.signal);
        if (controller.signal.aborted) return;
        have = [...have, ...(view.events ?? [])];
        setEvents(have);
        setDetail(view);
        setError(null);
        if (view.run.state !== "running") delay = 8_000;
      } catch (err) {
        if (controller.signal.aborted) return;
        setError(err instanceof Error ? err.message : String(err));
        delay = 5_000;
      }
      timer = setTimeout(tick, delay);
    };
    void tick();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [id, nudge]);

  return { detail, events, error };
}
