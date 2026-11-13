import { useEffect, useMemo, useState } from "react";

export const ROUTES = [
  { id: "overview", label: "Overview" },
  { id: "workloads", label: "Workloads" },
  { id: "explore", label: "Find apps" },
  { id: "automation", label: "Automation" },
  { id: "runs", label: "Runs" },
  { id: "cluster", label: "Cluster" },
  { id: "settings", label: "Settings" },
] as const;

export type RouteId = (typeof ROUTES)[number]["id"];

export interface Destination {
  path: RouteId;
  params?: Record<string, string>;
}

export interface AppLocation {
  path: RouteId;
  params: URLSearchParams;
}

// Hash routing keeps the UI deployable as plain static files served by the
// agent, with no server-side route handling, and makes every view linkable.
export function hrefFor({ path, params }: Destination): string {
  const query = params ? `?${new URLSearchParams(params)}` : "";
  return `#/${path}${query}`;
}

function parse(hash: string): AppLocation {
  const [rawPath, query = ""] = hash.replace(/^#\/?/, "").split("?");
  // "fleet" was the Cluster page's old name, and the assistant used to be a page: keep
  // old links and bookmarks landing somewhere sensible.
  const wanted = rawPath === "fleet" ? "cluster" : rawPath === "assistant" ? "overview" : rawPath;
  const path = ROUTES.find((route) => route.id === wanted)?.id ?? "overview";
  return { path, params: new URLSearchParams(query) };
}

export function useLocation(): AppLocation {
  const [hash, setHash] = useState(() => window.location.hash);
  useEffect(() => {
    const onChange = () => setHash(window.location.hash);
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return useMemo(() => parse(hash), [hash]);
}
