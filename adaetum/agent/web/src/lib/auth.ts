import { useSyncExternalStore } from "react";

// Access to the agent. When an agent is reachable from the network it requires an
// access token on every API call (see cmd/adaetum-agent/exposure.go); the page
// itself is public static files with no host data in them. When the agent is only
// reachable from the machine itself it asks for nothing, and this module stays out
// of the way: the login screen appears only if the agent answers 401.

// The token lives in sessionStorage: it survives a reload but not closing the
// tab, and is never written anywhere that outlives the session. It is deliberately
// not a cookie, so no other site can make the browser send it. Storage can be
// blocked, so every access is guarded, and the value is also held in memory so the
// page works without it. It is one shared value with subscribers, so every part of
// the UI always agrees on it.
const TOKEN_KEY = "adaetum.agentToken";
let memoryToken = "";
const listeners = new Set<() => void>();

export const tokenStore = {
  get(): string {
    try {
      return window.sessionStorage.getItem(TOKEN_KEY) ?? memoryToken;
    } catch {
      return memoryToken;
    }
  },
  set(token: string) {
    memoryToken = token;
    try {
      if (token) window.sessionStorage.setItem(TOKEN_KEY, token);
      else window.sessionStorage.removeItem(TOKEN_KEY);
    } catch {
      /* the token is still held in memory for this page */
    }
    listeners.forEach((notify) => notify());
  },
  subscribe(listener: () => void): () => void {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },
};

// useToken re-renders when the token is set or cleared.
export function useToken(): string {
  return useSyncExternalStore(tokenStore.subscribe, tokenStore.get, () => "");
}

// AuthRequiredError means the agent wants a token (or did not accept the one sent).
export class AuthRequiredError extends Error {
  constructor() {
    super("This agent needs an access token.");
  }
}

// authedFetch sends the token, when there is one, and turns a 401 into
// AuthRequiredError so the app can show the sign-in screen instead of an error.
export async function authedFetch(input: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  const token = tokenStore.get();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(input, { ...init, headers });
  if (response.status === 401) throw new AuthRequiredError();
  return response;
}
