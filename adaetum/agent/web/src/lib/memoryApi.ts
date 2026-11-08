import { authedFetch } from "./auth";

// Client for the agent's memory (agent/internal/memory).

export interface Memory {
  id: string;
  text: string;
  tags: string[];
  source: string;
  created: string;
  status: "searchable" | "queued" | "failed";
}

export interface SearchHit extends Memory {
  score: number;
  match: "semantic" | "keyword";
}

export interface MemoryStatus {
  embedder: string;
  total: number;
  embedded: number;
  queued: number;
  failed: number;
  degraded: boolean;
  lastError?: string;
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

async function json<T>(response: Response, fallback: string): Promise<T> {
  if (!response.ok) throw await failure(response, fallback);
  return response.json();
}

export const listMemories = async (signal?: AbortSignal) =>
  (await json<{ memories: Memory[] }>(await authedFetch("/api/v1/memory?limit=100", { signal }), "Could not read memories")).memories;

export const memoryStatus = async (signal?: AbortSignal) =>
  json<MemoryStatus>(await authedFetch("/api/v1/memory/status", { signal }), "Could not read the memory status");

export const searchMemories = async (query: string) =>
  json<{ results: SearchHit[]; degraded: boolean; embedder: string }>(
    await authedFetch("/api/v1/memory/search", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query, limit: 10 }) }),
    "Could not search",
  );

export const remember = async (text: string, tags: string[]) =>
  json<Memory>(
    await authedFetch("/api/v1/memory", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text, tags, source: "web" }) }),
    "Could not save that",
  );

export const forget = async (id: string) => {
  const response = await authedFetch(`/api/v1/memory/${encodeURIComponent(id)}`, { method: "DELETE" });
  if (!response.ok) throw await failure(response, "Could not forget that");
};

export const retryFailed = async () =>
  json<{ requeued: number }>(await authedFetch("/api/v1/memory/retry", { method: "POST" }), "Could not retry");
