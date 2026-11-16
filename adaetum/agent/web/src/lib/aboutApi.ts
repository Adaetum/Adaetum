import { useEffect, useState } from "react";
import { authedFetch } from "./auth";

// "About this app" text. It is written into the cluster repository (pods/<namespace>/<app>.about.yaml,
// by a person or by the describe-pod-app skill) and read back by the agent; this page only shows it.

export interface AboutDocument {
  summary: string;
  facts?: { label: string; value: string }[];
  watch?: string[];
  written: { by: "person" | "assistant"; date: string; version?: string };
}

export interface AboutResult {
  about: AboutDocument | null;
  problem?: string;
}

async function fetchAbout(name: string, signal: AbortSignal): Promise<AboutResult> {
  const response = await authedFetch(`/api/v1/about/${encodeURIComponent(name)}`, { signal });
  if (!response.ok) return { about: null };
  return (await response.json()) as AboutResult;
}

export type AboutState = { loading: true } | { loading: false; result: AboutResult };

// useAbout asks for each name an app goes by (its deployments' names, then its own) and
// keeps the first description it finds. It is off when the setting is off, so nothing is
// requested.
export function useAbout(names: string[], enabled: boolean): AboutState {
  const [state, setState] = useState<AboutState>({ loading: true });
  const key = names.join("\n");
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    setState({ loading: true });
    Promise.all(key.split("\n").filter(Boolean).map((name) => fetchAbout(name, controller.signal).catch(() => ({ about: null }) as AboutResult)))
      .then((results) => {
        if (controller.signal.aborted) return;
        setState({ loading: false, result: results.find((r) => r.about || r.problem) ?? { about: null } });
      })
      .catch(() => {
        if (!controller.signal.aborted) setState({ loading: false, result: { about: null } });
      });
    return () => controller.abort();
  }, [key, enabled]);
  return state;
}
