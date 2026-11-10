import { useCallback, useEffect, useRef, useState } from "react";
import { AuthRequiredError, authedFetch } from "./auth";

// Client for the in-page assistant (agent/internal/assistant).

export interface AssistantStatus {
  enabled: boolean;
  reason?: string;
  model?: string;
  // Where conversations are sent: "Anthropic", or an OpenAI-compatible service.
  provider?: string;
  endpoint?: string;
}

export interface ToolCall {
  id: string;
  name: string;
  input: unknown;
  mutates: boolean;
  status: "pending" | "running" | "done" | "error" | "denied";
  result?: string;
}

export interface Item {
  seq: number;
  kind: "user" | "assistant" | "tool" | "error" | "notice";
  text?: string;
  tool?: ToolCall;
}

export type ConversationState = "idle" | "working" | "waiting_approval";

interface View {
  id: string;
  state: ConversationState;
  items: Item[];
  next: number;
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

export async function fetchAssistantStatus(signal?: AbortSignal): Promise<AssistantStatus> {
  const response = await authedFetch("/api/v1/assistant/status", { signal });
  if (!response.ok) throw await failure(response, "Could not read the assistant's status");
  return response.json();
}

async function post(path: string, body?: unknown): Promise<Response> {
  const response = await authedFetch(path, {
    method: "POST",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) throw await failure(response, "The assistant could not do that");
  return response;
}

const KEY = "adaetum.assistantConversation";

function storedId(): string | null {
  try {
    return window.sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}
function storeId(id: string | null) {
  try {
    if (id) window.sessionStorage.setItem(KEY, id);
    else window.sessionStorage.removeItem(KEY);
  } catch {
    /* kept in memory for this page only */
  }
}

export interface ConversationHook {
  items: Item[];
  state: ConversationState;
  error: string | null;
  // The agent wants a token (the status is public, a conversation is not).
  authRequired: boolean;
  signedIn: () => void;
  // `page` is what the person has on screen, when they have chosen to share it.
  send: (text: string, page?: string) => Promise<void>;
  // Drops whatever conversation there was and opens a new one with this first message.
  startNew: (text: string, page?: string) => Promise<void>;
  decide: (toolId: string, approve: boolean) => Promise<void>;
  stop: () => Promise<void>;
  reset: () => void;
}

// useConversation keeps one conversation, polling for new transcript items while the
// assistant is working or waiting for an approval, and slowly otherwise. Conversations
// live in the agent's memory only, so a conversation the agent no longer has is
// replaced by a fresh one.
export function useConversation(enabled: boolean): ConversationHook {
  const [id, setId] = useState<string | null>(storedId());
  const [items, setItems] = useState<Item[]>([]);
  const [state, setState] = useState<ConversationState>("idle");
  const [error, setError] = useState<string | null>(null);
  const [authRequired, setAuthRequired] = useState(false);
  const next = useRef(0);
  // Read by the poll loop without restarting it: while nobody is looking (the drawer is
  // closed) an idle conversation is not polled, but a working one still is.
  const enabledRef = useRef(enabled);
  const stateRef = useRef(state);
  enabledRef.current = enabled;
  stateRef.current = state;
  // The transcript is append-only except for tool calls, whose status and result change
  // as they run. While any call is unfinished the whole transcript is re-read, so the
  // page never shows an old "Waiting for you" after the change was approved.
  const open = useRef(false);
  const fail = useCallback((err: unknown) => {
    if (err instanceof AuthRequiredError) setAuthRequired(true);
    else setError(err instanceof Error ? err.message : String(err));
  }, []);

  const ensure = useCallback(async (): Promise<string> => {
    if (id) return id;
    const created = await (await post("/api/v1/assistant/conversations")).json();
    storeId(created.id);
    setId(created.id);
    return created.id;
  }, [id]);

  useEffect(() => {
    if (!id) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      let delay = 4000;
      if (!enabledRef.current && stateRef.current === "idle") {
        // No request is made; checking often only means opening the drawer shows the
        // conversation at once instead of a few seconds later.
        timer = setTimeout(tick, 500);
        return;
      }
      try {
        const after = open.current ? 0 : next.current;
        const response = await authedFetch(`/api/v1/assistant/conversations/${id}?after=${after}`, { quiet: true });
        if (response.status === 404) {
          // The agent restarted or dropped it: start over cleanly.
          storeId(null);
          next.current = 0;
          setItems([]);
          setState("idle");
          setId(null);
          return;
        }
        if (!response.ok) throw await failure(response, "Could not read the conversation");
        const view: View = await response.json();
        if (after === 0) setItems(view.items);
        else if (view.items.length > 0) setItems((current) => [...current, ...view.items]);
        next.current = view.next;
        const everything = after === 0 ? view.items : null;
        if (everything) open.current = everything.some((item) => item.tool && (item.tool.status === "pending" || item.tool.status === "running"));
        else open.current = view.items.some((item) => item.tool && (item.tool.status === "pending" || item.tool.status === "running")) || open.current;
        setState(view.state);
        setError(null);
        delay = view.state === "idle" ? 4000 : 700;
      } catch (err) {
        if (!stopped) fail(err);
      }
      if (!stopped) timer = setTimeout(tick, delay);
    };
    tick();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [id, fail]);

  const send = useCallback(
    async (text: string, page?: string) => {
      setError(null);
      try {
        const conversation = await ensure();
        await post(`/api/v1/assistant/conversations/${conversation}/messages`, { text, page });
        setState("working"); // poll quickly until it says otherwise
      } catch (err) {
        fail(err);
      }
    },
    [ensure, fail],
  );

  const startNew = useCallback(
    async (text: string, page?: string) => {
      setError(null);
      try {
        const created = await (await post("/api/v1/assistant/conversations")).json();
        storeId(created.id);
        next.current = 0;
        open.current = false;
        setItems([]);
        setState("working"); // poll quickly until it says otherwise
        setId(created.id);
        await post(`/api/v1/assistant/conversations/${created.id}/messages`, { text, page });
      } catch (err) {
        fail(err);
      }
    },
    [fail],
  );

  const decide = useCallback(
    async (toolId: string, approve: boolean) => {
      if (!id) return;
      try {
        await post(`/api/v1/assistant/conversations/${id}/approvals/${encodeURIComponent(toolId)}`, { approve });
      } catch (err) {
        fail(err);
      }
    },
    [id, fail],
  );

  const stop = useCallback(async () => {
    if (!id) return;
    try {
      await post(`/api/v1/assistant/conversations/${id}/cancel`);
    } catch (err) {
      fail(err);
    }
  }, [id, fail]);

  const reset = useCallback(() => {
    storeId(null);
    next.current = 0;
    open.current = false;
    setItems([]);
    setState("idle");
    setError(null);
    setId(null);
  }, []);

  const signedIn = useCallback(() => setAuthRequired(false), []);
  return { items, state, error, authRequired, signedIn, send, startNew, decide, stop, reset };
}
