import { trackBusy } from "./busy";
import { useCallback, useEffect, useState } from "react";
import { authedFetch, tokenStore, useToken } from "./auth";

// Signing in with an account. The accounts live in the auth service (auth/), reached
// through the agent; these calls go to the agent's own address, so they work on any host
// or the cluster agent, whichever name the page was opened by. Passwords are sent only
// to those calls and never kept: the browser holds a cookie the agent's auth service sets.

export interface AuthState {
  enabled: boolean;
  mode?: "primary" | "replica";
  // Nobody has an account yet: offer the first-run setup (needs the code shown on the machine).
  needsSetup?: boolean;
  hasAccounts?: boolean;
  // Accounts exist but the sign-in service is not answering; only the access token works.
  unavailable?: boolean;
}

export async function fetchAuthState(): Promise<AuthState> {
  try {
    const response = await fetch("/api/auth-state");
    if (!response.ok || !(response.headers.get("content-type") ?? "").includes("json")) return { enabled: false };
    return await response.json();
  } catch {
    return { enabled: false };
  }
}

export type SignInResult = "ok" | "wrong" | "slow-down" | "unavailable";

export async function signInWithAccount(email: string, password: string): Promise<SignInResult> {
  try {
    const response = await trackBusy(fetch("/api/auth/sign-in/email", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: email.trim(), password }),
    }));
    if (response.ok) return "ok";
    if (response.status === 429) return "slow-down";
    if (response.status >= 500) return "unavailable";
    return "wrong";
  } catch {
    return "unavailable";
  }
}

export interface SetupInput {
  code: string;
  name: string;
  email: string;
  password: string;
}

// setUpFirstOwner returns an empty string on success, or a sentence to show.
export async function setUpFirstOwner(input: SetupInput): Promise<string> {
  try {
    const response = await trackBusy(fetch("/api/setup", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input) }));
    if (response.status === 201) return "";
    const body = await response.json().catch(() => ({}));
    switch (body?.error) {
      case "wrong_code": return "That code isn't right. It is shown on the server itself.";
      case "too_many_tries": return "Too many wrong tries. Wait a minute and try again.";
      case "already_set_up": return "Someone has already set this up. Sign in instead.";
      case "waiting_for_cluster": return "This server is waiting for accounts from the cluster.";
      default: return typeof body?.message === "string" ? body.message : "That did not work. Check what you typed.";
    }
  } catch {
    return "The sign-in service is not answering.";
  }
}

export async function signOutAccount(): Promise<void> {
  try {
    await fetch("/api/auth/sign-out", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
  } catch {
    /* the cookie may still be cleared; the reload below shows the truth */
  }
}

export interface Me {
  id?: string;
  email?: string;
  name?: string;
  // owner, admin or member. The access token counts as an owner.
  role: string;
  viaToken?: boolean;
}

// useMe says who the agent thinks you are, or null when it has no accounts (then there is
// no one to name). `refresh` re-reads it after signing in or out.
export function useMe(): { me: Me | null; refresh: () => void } {
  const token = useToken();
  const [me, setMe] = useState<Me | null>(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let current = true;
    void (async () => {
      try {
        const response = await authedFetch("/api/v1/me");
        if (!response.ok || !(response.headers.get("content-type") ?? "").includes("json")) return current && setMe(null);
        const body = (await response.json()) as Me;
        current && setMe(body.role ? body : null);
      } catch {
        current && setMe(null);
      }
    })();
    return () => {
      current = false;
    };
  }, [token, tick]);
  return { me, refresh: useCallback(() => setTick((n) => n + 1), []) };
}

// signOut ends whichever way you are signed in, then reloads so nothing from the old
// session stays on screen.
export async function signOutEverything(): Promise<void> {
  tokenStore.set("");
  await signOutAccount();
  window.location.reload();
}

// ---- People (owner view) --------------------------------------------------------

export interface Person {
  id: string;
  name: string;
  email: string;
  role: "owner" | "admin" | "member";
  createdAt?: string;
}

async function reason(response: Response, fallback: string): Promise<Error> {
  try {
    const body = await response.json();
    if (typeof body?.message === "string") return new Error(body.message);
    if (body?.error === "accounts_are_changed_on_the_cluster") return new Error("Accounts are changed on the cluster, not on this server.");
    if (body?.error === "last_owner") return new Error("There must always be at least one owner.");
  } catch {
    /* not JSON */
  }
  return new Error(`${fallback} (${response.status})`);
}

export async function fetchPeople(signal?: AbortSignal): Promise<{ mode: string; people: Person[] }> {
  const response = await authedFetch("/api/v1/people", { signal });
  if (!response.ok) throw await reason(response, "Could not read the list of people");
  return response.json();
}

const send = async (method: string, path: string, body: unknown | undefined, fallback: string) => {
  const response = await authedFetch(path, { method, headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
  if (!response.ok) throw await reason(response, fallback);
};

export const addPerson = (person: { name: string; email: string; password: string; role: string }) => send("POST", "/api/v1/people", person, "Could not add that person");
export const removePerson = (id: string) => send("DELETE", `/api/v1/people/${encodeURIComponent(id)}`, undefined, "Could not remove that person");
export const setPersonRole = (id: string, role: string) => send("POST", `/api/v1/people/${encodeURIComponent(id)}/role`, { role }, "Could not change that role");
export const signPersonOut = (id: string) => send("POST", `/api/v1/people/${encodeURIComponent(id)}/revoke-sessions`, {}, "Could not sign that person out");
