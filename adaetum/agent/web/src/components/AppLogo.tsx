import { useEffect, useState } from "react";
import { authedFetch } from "../lib/auth";

// Logos come from the agent (never straight from a third party), and need the login
// token, so they are fetched and shown from a local object URL. Each picture is asked
// for once per page load; "no logo" is remembered too, "try later" is not.
const pictures = new Map<string, Promise<string | null>>();

function load(query: string): Promise<string | null> {
  let picture = pictures.get(query);
  if (!picture) {
    const asked = authedFetch(`/api/v1/catalog/logo?${query}`)
      .then(async (response) => {
        if (response.ok) return URL.createObjectURL(await response.blob());
        // 404 means this app has no logo. Anything else means "not now", so a later
        // visit to the page may ask again instead of inheriting the miss.
        if (response.status !== 404) pictures.delete(query);
        return null;
      })
      .catch(() => {
        pictures.delete(query);
        return null;
      });
    picture = asked;
    pictures.set(query, picture);
  }
  return picture;
}

// Either a known Artifact Hub image id (search results) or an app name (installed apps,
// matched to a trusted chart of exactly that name by the agent).
export type LogoSource = { image: string } | { name: string; chart?: string; repo?: string; parts?: string[] };

function queryFor(source: LogoSource): string {
  const query = new URLSearchParams();
  if ("image" in source) {
    query.set("image", source.image);
  } else {
    query.set("name", source.name);
    if (source.chart && source.repo) {
      query.set("chart", source.chart);
      query.set("repo", source.repo);
    }
    // Component names the agent may try as logo names (it ignores any beyond a few).
    (source.parts ?? []).slice(0, 8).forEach((part) => query.append("part", part));
  }
  return query.toString();
}

// A null source (an image with no known logo) shows the letter without asking.
export function AppLogo({ source, label, size = 40 }: { source: LogoSource | null; label: string; size?: number }) {
  const query = !source ? "" : queryFor(source);
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    let current = true;
    setUrl(null);
    if (!query) return;
    void load(query).then((found) => current && setUrl(found));
    return () => {
      current = false;
    };
  }, [query]);

  // Without a picture the first letter stands in, so cards stay the same shape.
  return url ? (
    <img className="app-logo" src={url} alt="" width={size} height={size} style={{ width: size, height: size }} />
  ) : (
    <span className="app-logo app-logo-fallback" aria-hidden style={{ width: size, height: size, fontSize: size * 0.45 }}>
      {label.trim().charAt(0).toUpperCase() || "?"}
    </span>
  );
}
