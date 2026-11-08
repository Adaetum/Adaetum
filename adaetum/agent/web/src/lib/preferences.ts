import { useSyncExternalStore } from "react";

// Interface preferences kept in this browser, like the colour theme. They shape how the
// dashboard looks and never change what the agent does, so they need no sign-in and no
// server copy. Storage can throw (private windows, blocked site data), so every access is
// guarded and the choice still holds for the session.

function preference<T extends string>(key: string, fallback: T, allowed: readonly T[]) {
  const read = (): T => {
    try {
      const saved = window.localStorage.getItem(key) as T | null;
      return saved !== null && allowed.includes(saved) ? saved : fallback;
    } catch {
      return fallback;
    }
  };
  let value = read();
  const listeners = new Set<() => void>();
  const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  };
  return {
    use: () => useSyncExternalStore(subscribe, () => value),
    get: () => value,
    set: (next: T) => {
      value = next;
      try {
        window.localStorage.setItem(key, next);
      } catch {
        /* applies for this session only */
      }
      listeners.forEach((listener) => listener());
    },
  };
}

const advanced = preference("adaetum.advancedUser", "0", ["0", "1"] as const);

export function setAdvancedUser(next: boolean): void {
  advanced.set(next ? "1" : "0");
}

// Advanced user mode: the "Advanced" sections stay open instead of tucked away. Other
// things meant only for people who want every detail can key off the same switch.
export function useAdvancedUser(): boolean {
  return advanced.use() === "1";
}

// How the assistant panel sits with the page. "split" makes room beside it, so the page
// stays usable while the assistant works; "over" lays it on top of the page.
export type AssistantMode = "split" | "over";
const assistantMode = preference<AssistantMode>("adaetum.assistantMode", "split", ["split", "over"] as const);
export const useAssistantMode = assistantMode.use;
export const setAssistantMode = assistantMode.set;

// Whether the assistant panel was open when the page was last used, so it is still there
// after a reload.
const assistantOpen = preference("adaetum.assistantOpen", "0", ["0", "1"] as const);
export const wasAssistantOpen = () => assistantOpen.get() === "1";
export const rememberAssistantOpen = (open: boolean) => assistantOpen.set(open ? "1" : "0");

// Whether what is on screen is sent along with each message to the assistant.
const sharePage = preference("adaetum.assistantSharesPage", "1", ["0", "1"] as const);
export const useSharePage = () => sharePage.use() === "1";
export const getSharePage = () => sharePage.get() === "1";
export const setSharePage = (share: boolean) => sharePage.set(share ? "1" : "0");
