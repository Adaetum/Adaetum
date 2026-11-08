import { Badge, Button, Callout, Card, Code, Flex, Heading, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, CircleCheck, MessageCircle, Search, TriangleAlert } from "lucide-react";
import { FormEvent, useRef, useState } from "react";
import { AppLogo } from "../components/AppLogo";
import { PageHeader } from "../components/PageHeader";
import { AppPreview, CatalogEntry, CatalogResults, PreviewFlag, previewInstall, searchCatalog } from "../lib/catalogApi";
import { AuthRequiredError, useToken } from "../lib/auth";
import { plural } from "../lib/format";
import { Login } from "./Login";
import { useAssistant } from "../components/assistant/AssistantProvider";

// Explore finds apps in public sources (Helm charts first, container images as the
// fallback) and shows a PREVIEW of adding one. It installs nothing: a preview is the
// files that would go into the recovery repository, with what to know about them.
// The assistant uses the same two searches, so both see the same results.
export function Explore() {
  const token = useToken();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<CatalogResults | null>(null);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [needsSignIn, setNeedsSignIn] = useState(false);
  const [preview, setPreview] = useState<AppPreview | null>(null);
  const [previewing, setPreviewing] = useState<string | null>(null);
  // A newer action replaces an older one, so a slow answer never overwrites a fresh one.
  const latest = useRef(0);

  const fail = (err: unknown) => {
    if (err instanceof AuthRequiredError) setNeedsSignIn(true);
    else setError(err instanceof Error ? err.message : String(err));
  };

  const search = async (event: FormEvent) => {
    event.preventDefault();
    const ticket = ++latest.current;
    setSearching(true);
    setError(null);
    setPreview(null);
    try {
      const found = await searchCatalog(query);
      if (ticket === latest.current) setResults(found);
    } catch (err) {
      if (ticket === latest.current) fail(err);
    } finally {
      if (ticket === latest.current) setSearching(false);
    }
  };

  const show = async (entry: CatalogEntry) => {
    const ticket = ++latest.current;
    setPreviewing(entry.id);
    setError(null);
    try {
      const drafted = await previewInstall(entry);
      if (ticket === latest.current) setPreview(drafted);
    } catch (err) {
      if (ticket === latest.current) fail(err);
    } finally {
      if (ticket === latest.current) setPreviewing(null);
    }
  };

  return (
    <>
      <PageHeader title="Find apps" description="Search public apps. You see who publishes it and what to check first; nothing is installed from this page." />
      {needsSignIn ? (
        <Login rejected={token !== ""} onSignedIn={() => setNeedsSignIn(false)} />
      ) : (
        <Flex direction="column" gap="4">
          <form onSubmit={search} role="search">
            <Flex gap="2">
              <TextField.Root
                style={{ flex: 1 }}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="What do you want to run? For example: uptime monitor, wiki, photo gallery"
                aria-label="Search for an app"
                maxLength={80}
              >
                <TextField.Slot><Search size={16} aria-hidden /></TextField.Slot>
              </TextField.Root>
              <Button type="submit" disabled={searching || query.trim().length < 2}>{searching ? "Searching…" : "Search"}</Button>
            </Flex>
          </form>

          {error && (
            <Callout.Root color="red" role="alert">
              <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
              <Callout.Text>{error}</Callout.Text>
            </Callout.Root>
          )}

          <div aria-live="polite">{preview && <Preview preview={preview} onClose={() => setPreview(null)} />}</div>

          {results && (
            <>
              {results.notes?.map((note) => (
                <Callout.Root key={note} color="amber" role="status" size="1">
                  <Callout.Icon><TriangleAlert size={14} /></Callout.Icon>
                  <Callout.Text>{note}</Callout.Text>
                </Callout.Root>
              ))}
              <Section
                title="Ready-made packages (Helm charts)"
                help="Made to be configured, so these are the better choice."
                entries={results.charts}
                previewing={previewing}
                onPreview={show}
              />
              <Section
                title="Plain container images"
                help="A fallback. The agent can only guess how to run these, so they need more checking."
                entries={results.images}
                previewing={previewing}
                onPreview={show}
              />
            </>
          )}
        </Flex>
      )}
    </>
  );
}

function Section({ title, help, entries, previewing, onPreview }: { title: string; help: string; entries: CatalogEntry[]; previewing: string | null; onPreview: (entry: CatalogEntry) => void }) {
  return (
    <section aria-label={title}>
      <Heading size="3" as="h2">{title}</Heading>
      <Text size="2" color="gray" as="p" mb="2">{help}</Text>
      {entries.length === 0 ? (
        <Text size="2" color="gray">Nothing found here.</Text>
      ) : (
        <Flex direction="column" gap="2">
          {entries.map((entry) => (
            <Result key={`${entry.kind}:${entry.id}`} entry={entry} busy={previewing === entry.id} onPreview={() => onPreview(entry)} />
          ))}
        </Flex>
      )}
    </section>
  );
}

function Result({ entry, busy, onPreview }: { entry: CatalogEntry; busy: boolean; onPreview: () => void }) {
  const assistant = useAssistant();
  const ask = `I'm thinking of adding ${entry.name} (${entry.kind === "chart" ? "Helm chart" : "container image"} ${entry.id} from ${entry.source}). Is it a good choice, and what would adding it involve?`;
  return (
    <Card size="2">
      <Flex direction="column" gap="2">
        <Flex justify="between" align="start" gap="3" wrap="wrap">
          <Flex gap="3" align="start" style={{ minWidth: 0, flex: 1 }}>
          <AppLogo source={entry.logo ? { image: entry.logo } : null} label={entry.name} />
          <Flex direction="column" gap="1" style={{ minWidth: 0, flex: 1 }}>
            <Flex gap="2" align="center" wrap="wrap">
              <Text size="3" weight="medium">{entry.name}</Text>
              {entry.official && <Badge color="green" variant="soft">Official</Badge>}
              {entry.verified && <Badge color="green" variant="soft">Verified publisher</Badge>}
              {entry.signed && <Badge color="green" variant="soft">Signed</Badge>}
              {entry.deprecated && <Badge color="red" variant="soft">Retired by its publisher</Badge>}
              {!entry.official && !entry.verified && <Badge color="gray" variant="outline">Not verified</Badge>}
            </Flex>
            <Text size="1" color="gray">
              {entry.publisher} · {entry.source}
              {entry.version ? ` · version ${entry.version}` : ""}
              {entry.stars > 0 ? ` · ${plural(entry.stars, "star")}` : ""}
            </Text>
          </Flex>
          </Flex>
          <Flex gap="2">
            <Button size="1" variant="soft" onClick={onPreview} disabled={busy}>{busy ? "Looking…" : "See details"}</Button>
            <Button size="1" variant="ghost" color="gray" onClick={() => assistant.ask(ask)}>
              <MessageCircle size={14} aria-hidden /> Ask the assistant
            </Button>
          </Flex>
        </Flex>
        {entry.description && <Text size="2" style={{ overflowWrap: "anywhere" }}>{entry.description}</Text>}
        <Text size="1"><a href={entry.page} target="_blank" rel="noreferrer noopener">Read more on {entry.source} (opens a new tab)</a></Text>
      </Flex>
    </Card>
  );
}

const FLAG_ICON: Record<PreviewFlag["level"], { icon: typeof CircleCheck; color: string; label: string }> = {
  good: { icon: CircleCheck, color: "var(--green-11)", label: "Good" },
  warn: { icon: TriangleAlert, color: "var(--amber-11)", label: "Check" },
  bad: { icon: CircleAlert, color: "var(--red-11)", label: "Problem" },
};

function Preview({ preview, onClose }: { preview: AppPreview; onClose: () => void }) {
  return (
    <Card size="3" style={{ borderLeft: "3px solid var(--accent-9)" }}>
      <Flex direction="column" gap="3">
        <Flex justify="between" align="center" gap="2">
          <Heading size="4" as="h2">What you'd be starting from: {preview.entry.name}</Heading>
          <Button size="1" variant="ghost" color="gray" onClick={onClose}>Close</Button>
        </Flex>
        <Callout.Root color="green" size="1" role="status">
          <Callout.Icon><CircleCheck size={14} /></Callout.Icon>
          <Callout.Text>Nothing has been installed.</Callout.Text>
        </Callout.Root>
        <Text size="2">{preview.summary}</Text>

        <Flex direction="column" gap="1" role="list" aria-label="Things to know">
          {preview.flags.map((flag) => {
            const { icon: Icon, color, label } = FLAG_ICON[flag.level];
            return (
              <Flex key={flag.text} gap="2" align="start" role="listitem">
                <span style={{ color, display: "flex", paddingTop: 2 }}><Icon size={16} aria-label={label} /></span>
                <Text size="2">{flag.text}</Text>
              </Flex>
            );
          })}
        </Flex>

        {preview.needs.length > 0 && (
          <Flex direction="column" gap="1">
            <Text size="2" weight="medium">Still for you to check</Text>
            <ul style={{ margin: 0, paddingLeft: "1.2rem" }}>
              {preview.needs.map((need) => (
                <li key={need}><Text size="2">{need}</Text></li>
              ))}
            </ul>
          </Flex>
        )}

        <Flex direction="column" gap="2">
          <Text size="2" weight="medium">A rough starting point (not yet in Adaetum's own layout)</Text>
          {preview.files.map((file) => (
            <details key={file.path}>
              <summary><Code size="2">{file.path}</Code></summary>
              <pre className="raw-json" tabIndex={0}>{file.content}</pre>
              <Button size="1" variant="soft" color="gray" onClick={() => void navigator.clipboard?.writeText(file.content)}>Copy</Button>
            </details>
          ))}
        </Flex>
      </Flex>
    </Card>
  );
}
