import { authedFetch } from "./auth";

// Client for the app explorer (agent/internal/catalog). Search and preview only: the
// agent reads public chart and image sources and drafts files; nothing is installed.

export interface CatalogEntry {
  kind: "chart" | "image";
  id: string;
  name: string;
  description: string;
  publisher: string;
  version?: string;
  appVersion?: string;
  verified: boolean;
  official: boolean;
  signed?: boolean;
  deprecated?: boolean;
  stars: number;
  pulls?: number;
  // Artifact Hub image id; pass it to AppLogo.
  logo?: string;
  source: string;
  page: string;
}

export interface CatalogResults {
  query: string;
  charts: CatalogEntry[];
  images: CatalogEntry[];
  notes?: string[];
}

export interface PreviewFlag {
  level: "good" | "warn" | "bad";
  text: string;
}

export interface PreviewFile {
  path: string;
  content: string;
}

export interface AppPreview {
  entry: CatalogEntry;
  name: string;
  namespace: string;
  summary: string;
  flags: PreviewFlag[];
  needs: string[];
  files: PreviewFile[];
  applied: boolean;
}

async function read<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await authedFetch(path, { signal });
  if (!response.ok) {
    let message = `The agent could not do that (${response.status})`;
    try {
      const body = await response.json();
      if (typeof body?.error === "string" && body.error) message = body.error;
    } catch {
      /* not JSON */
    }
    throw new Error(message);
  }
  return response.json();
}

export function searchCatalog(query: string, signal?: AbortSignal): Promise<CatalogResults> {
  return read(`/api/v1/catalog/search?${new URLSearchParams({ q: query })}`, signal);
}

export function previewInstall(entry: CatalogEntry, signal?: AbortSignal): Promise<AppPreview> {
  return read(`/api/v1/catalog/preview?${new URLSearchParams({ kind: entry.kind, id: entry.id })}`, signal);
}
