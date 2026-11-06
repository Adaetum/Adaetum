import { authedFetch } from "./auth";
import { AssistantStatus } from "./assistantApi";

// Client for choosing which AI service the assistant uses
// (agent/internal/assistant/manager.go). The API key is only ever sent TO the agent.

export interface AssistantSettings {
  enabled: boolean;
  provider: string;
  url?: string;
  model?: string;
}

export interface Preset {
  id: string;
  label: string;
  url?: string;
  free: boolean;
  local: boolean;
  needsKey: boolean;
  hint: string;
}

export interface AssistantSettingsState {
  settings: AssistantSettings;
  status: AssistantStatus;
  // Where the key in use came from: "" (none), "entered" (typed in this page, saved in
  // the agent's database) or "startup" (given when the agent started). Never the key.
  keySource: "" | "entered" | "startup";
  presets: Preset[];
}

export interface AssistantTest {
  ok: boolean;
  error?: string;
  provider?: string;
  model?: string;
  endpoint?: string;
  latencyMs?: number;
  reply?: string;
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

const JSON_HEADERS = { "Content-Type": "application/json" };

export async function fetchAssistantSettings(signal?: AbortSignal): Promise<AssistantSettingsState> {
  const response = await authedFetch("/api/v1/assistant/settings", { signal });
  if (!response.ok) throw await failure(response, "Could not read the AI model settings");
  return response.json();
}

export async function testAssistantSettings(settings: AssistantSettings, apiKey: string): Promise<AssistantTest> {
  const response = await authedFetch("/api/v1/assistant/settings/test", {
    method: "POST",
    headers: JSON_HEADERS,
    body: JSON.stringify({ ...settings, apiKey: apiKey || undefined }),
  });
  if (!response.ok) throw await failure(response, "Could not run the test");
  return response.json();
}

export async function saveAssistantSettings(settings: AssistantSettings, apiKey: string, confirmExternal: boolean): Promise<void> {
  const response = await authedFetch("/api/v1/assistant/settings", {
    method: "PUT",
    headers: JSON_HEADERS,
    body: JSON.stringify({ ...settings, apiKey: apiKey || undefined, confirmExternal }),
  });
  if (!response.ok) throw await failure(response, "Could not change the setting");
}

export async function forgetAssistantKey(): Promise<void> {
  const response = await authedFetch("/api/v1/assistant/settings/key", { method: "DELETE" });
  if (!response.ok) throw await failure(response, "Could not forget the key");
}
