import { authedFetch } from "./auth";
import { MemoryStatus } from "./memoryApi";

// Client for where memory's embeddings come from (agent/internal/memory/manager.go).

export type EmbeddingKind = "builtin" | "service" | "openai" | "none";

export interface EmbeddingConfig {
  kind: EmbeddingKind;
  url?: string;
  model?: string;
}

export interface EmbeddingState {
  config: EmbeddingConfig;
  model: string;
  enabled: boolean;
  external: boolean;
  status: MemoryStatus;
  vectorsByModel: Record<string, number>;
  // Vectors made by a different model than the one in use.
  staleVectors: number;
  keyAvailable: boolean;
  defaultService: string;
}

export interface TestResult {
  ok: boolean;
  error?: string;
  model?: string;
  dim?: number;
  latencyMs?: number;
  semantic: boolean;
  related: number;
  unrelated: number;
  external: boolean;
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

export async function fetchEmbedding(signal?: AbortSignal): Promise<EmbeddingState> {
  const response = await authedFetch("/api/v1/embedding", { signal });
  if (!response.ok) throw await failure(response, "Could not read the embedding settings");
  return response.json();
}

export async function testEmbedding(config: EmbeddingConfig): Promise<TestResult> {
  const response = await authedFetch("/api/v1/embedding/test", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(config),
  });
  if (!response.ok) throw await failure(response, "Could not run the test");
  return response.json();
}

export async function saveEmbedding(config: EmbeddingConfig, confirmExternal: boolean): Promise<void> {
  const response = await authedFetch("/api/v1/embedding", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...config, confirmExternal }),
  });
  if (!response.ok) throw await failure(response, "Could not change the setting");
}

export async function reembed(): Promise<number> {
  const response = await authedFetch("/api/v1/embedding/reembed", { method: "POST" });
  if (!response.ok) throw await failure(response, "Could not re-embed");
  return (await response.json()).queued;
}
