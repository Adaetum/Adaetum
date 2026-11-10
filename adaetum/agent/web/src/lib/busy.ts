import { useSyncExternalStore } from "react";

// How many requests the user is waiting on. authedFetch counts every call except those
// marked `quiet`, which are the page's own background polling: a refresh every 30
// seconds is not something to wait for, and showing it would train people to ignore the
// indicator. The global throbber reads this; it is the one place that says "wait".
let pending = 0;
const listeners = new Set<() => void>();

function change(delta: number) {
  pending = Math.max(0, pending + delta);
  listeners.forEach((listener) => listener());
}

// trackBusy counts `work` while it runs, whether it succeeds or fails.
export async function trackBusy<T>(work: Promise<T>): Promise<T> {
  change(1);
  try {
    return await work;
  } finally {
    change(-1);
  }
}

export function useBusy(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => pending > 0,
  );
}
