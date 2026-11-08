import { Badge, Button, Callout, Card, Flex, Skeleton, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, Search } from "lucide-react";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { useNow } from "../app/useReport";
import { PageHeader } from "../components/PageHeader";
import { AuthRequiredError, useToken } from "../lib/auth";
import { formatAgo, plural } from "../lib/format";
import { Memory as MemoryItem, MemoryStatus, SearchHit, forget, listMemories, memoryStatus, remember, retryFailed, searchMemories } from "../lib/memoryApi";
import { Login } from "./Login";

// Memory is what the agent (and the assistant) can recall later by meaning. A memory
// is saved at once; making it searchable by meaning happens in the background and can
// be slow or fail without anything else noticing, so recall falls back to keywords.
export function Memory() {
  const token = useToken();
  const now = new Date(useNow(30_000));
  const [memories, setMemories] = useState<MemoryItem[] | null>(null);
  const [status, setStatus] = useState<MemoryStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [needsSignIn, setNeedsSignIn] = useState(false);
  const [hits, setHits] = useState<SearchHit[] | null>(null);
  const [query, setQuery] = useState("");
  const [draft, setDraft] = useState("");
  const [tags, setTags] = useState("");

  const load = useCallback(async () => {
    try {
      const [list, state] = await Promise.all([listMemories(), memoryStatus()]);
      setMemories(list);
      setStatus(state);
      setError(null);
      setNeedsSignIn(false);
    } catch (err) {
      if (err instanceof AuthRequiredError) setNeedsSignIn(true);
      else setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = setInterval(load, 10_000); // vectors arrive in the background
    return () => clearInterval(timer);
  }, [load, token]);

  const run = async (action: () => Promise<unknown>) => {
    setError(null);
    try {
      await action();
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!draft.trim()) return;
    void run(async () => {
      await remember(draft, tags.split(",").map((tag) => tag.trim()).filter(Boolean));
      setDraft("");
      setTags("");
    });
  };

  const search = (event: FormEvent) => {
    event.preventDefault();
    if (!query.trim()) {
      setHits(null);
      return;
    }
    void run(async () => setHits((await searchMemories(query)).results));
  };

  return (
    <>
      <PageHeader title="Memory" description="Things worth finding again: decisions, how servers are set up, why something was done." />
      {needsSignIn ? (
        <Login rejected={token !== ""} onSignedIn={() => void load()} />
      ) : !memories || !status ? (
        error ? <Callout.Root color="red" role="alert"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{error}</Callout.Text></Callout.Root> : <Skeleton height="160px" />
      ) : (
        <Flex direction="column" gap="5">
          <StatusLine status={status} onRetry={() => void run(retryFailed)} />
          {error && (
            <Callout.Root color="red" role="alert">
              <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
              <Callout.Text>{error}</Callout.Text>
            </Callout.Root>
          )}

          <form onSubmit={search}>
            <Flex gap="2">
              <TextField.Root style={{ flex: 1 }} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search by meaning, for example “why is the registry slow”" aria-label="Search memory">
                <TextField.Slot><Search size={14} aria-hidden /></TextField.Slot>
              </TextField.Root>
              <Button type="submit" variant="soft">Search</Button>
            </Flex>
          </form>

          {hits && (
            <Flex direction="column" gap="2">
              <Text size="2" weight="medium">{plural(hits.length, "result")}</Text>
              {hits.map((hit) => (
                <MemoryCard key={hit.id} memory={hit} now={now} note={hit.match === "semantic" ? `matched by meaning (${Math.round(hit.score * 100)}%)` : "matched by keyword"} onForget={() => void run(() => forget(hit.id))} />
              ))}
            </Flex>
          )}

          <Card size="2">
            <form onSubmit={save}>
              <Flex direction="column" gap="2">
                <Text size="2" weight="medium">Remember something</Text>
                <TextField.Root value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="Short and factual. No passwords or keys." aria-label="What to remember" />
                <TextField.Root value={tags} onChange={(event) => setTags(event.target.value)} placeholder="Tags, separated by commas (optional)" aria-label="Tags" />
                <div><Button type="submit" disabled={!draft.trim()}>Save</Button></div>
              </Flex>
            </form>
          </Card>

          <Flex direction="column" gap="2">
            <Text size="2" weight="medium">{plural(memories.length, "memory", "memories")}</Text>
            {memories.length === 0 && <Text size="2" color="gray">Nothing remembered yet.</Text>}
            {memories.map((memory) => (
              <MemoryCard key={memory.id} memory={memory} now={now} onForget={() => void run(() => forget(memory.id))} />
            ))}
          </Flex>
        </Flex>
      )}
    </>
  );
}

function StatusLine({ status, onRetry }: { status: MemoryStatus; onRetry: () => void }) {
  if (status.degraded || status.failed > 0) {
    return (
      <Callout.Root color="amber" highContrast role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>
          Search by meaning is {status.degraded ? "unavailable right now" : "behind"}
          {status.lastError ? ` (${status.lastError})` : ""}, so searches match keywords instead. Everything is still saved. <a href="#/settings?tab=embedding">Check the embedding setup</a>.
          {status.failed > 0 && <> {plural(status.failed, "memory", "memories")} gave up waiting. <Button size="1" variant="ghost" onClick={onRetry}>Try them again</Button></>}
        </Callout.Text>
      </Callout.Root>
    );
  }
  return (
    <Text size="1" color="gray">
      Embedding with {status.embedder}: {status.embedded} searchable by meaning{status.queued > 0 ? `, ${status.queued} waiting` : ""}.{" "}
      <a href="#/settings?tab=embedding">Change this</a>
    </Text>
  );
}

function MemoryCard({ memory, now, note, onForget }: { memory: MemoryItem; now: Date; note?: string; onForget: () => void }) {
  return (
    <Card size="1">
      <Flex direction="column" gap="1">
        <Text size="2" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{memory.text}</Text>
        <Flex justify="between" align="center" gap="2" wrap="wrap">
          <Flex gap="2" align="center" wrap="wrap">
            {memory.tags.map((tag) => <Badge key={tag} color="gray" variant="soft" size="1">{tag}</Badge>)}
            <Text size="1" color="gray">
              {note ? `${note} · ` : ""}saved {formatAgo(memory.created, now)} by {memory.source}
              {memory.status !== "searchable" ? ` · ${memory.status === "queued" ? "waiting to be searchable by meaning" : "keyword search only"}` : ""}
            </Text>
          </Flex>
          <Button size="1" variant="ghost" color="gray" onClick={onForget}>Forget</Button>
        </Flex>
      </Flex>
    </Card>
  );
}
