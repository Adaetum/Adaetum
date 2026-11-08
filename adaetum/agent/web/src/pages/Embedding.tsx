import { Button, Callout, Card, Checkbox, Code, Flex, Heading, RadioGroup, Skeleton, Text, TextField } from "@radix-ui/themes";
import { Check, CircleAlert } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { AuthRequiredError, useToken } from "../lib/auth";
import { EmbeddingConfig, EmbeddingKind, EmbeddingState, TestResult, fetchEmbedding, reembed, saveEmbedding, testEmbedding } from "../lib/embeddingApi";
import { plural } from "../lib/format";
import { Login } from "./Login";

const OPTIONS: { kind: EmbeddingKind; title: string; blurb: string }[] = [
  {
    kind: "service",
    title: "The embedding service in the cluster (recommended)",
    blurb: "A small separate program that understands meaning. It keeps nothing, so Kubernetes can move it to another server if one fails. Nothing leaves the cluster.",
  },
  {
    kind: "openai",
    title: "Another service",
    blurb: "Any outside service that speaks the OpenAI embeddings format. What you remember is sent to it, so it asks you to confirm.",
  },
  {
    kind: "builtin",
    title: "Built in (for trying things out)",
    blurb: "Matches words, not meaning: “disk” will not find “storage”. Needs nothing installed.",
  },
  {
    kind: "none",
    title: "Off",
    blurb: "Memories are found by keyword only.",
  },
];

// Embedding is where you choose what turns your memories into something searchable by
// meaning: the dedicated service in the cluster, another service, or nothing. Every
// choice is tested before it is used, and a change never loses a memory.
export function Embedding() {
  const token = useToken();
  const [state, setState] = useState<EmbeddingState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [needsSignIn, setNeedsSignIn] = useState(false);
  const [kind, setKind] = useState<EmbeddingKind>("service");
  const [url, setUrl] = useState("");
  const [model, setModel] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [test, setTest] = useState<TestResult | null>(null);
  const [busy, setBusy] = useState<"test" | "save" | "reembed" | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const next = await fetchEmbedding();
      setState(next);
      setError(null);
      setNeedsSignIn(false);
      return next;
    } catch (err) {
      if (err instanceof AuthRequiredError) setNeedsSignIn(true);
      else setError(err instanceof Error ? err.message : String(err));
      return null;
    }
  }, []);

  // Start the form at what is in use, once.
  useEffect(() => {
    void load().then((loaded) => {
      if (!loaded) return;
      setKind(loaded.config.kind);
      setUrl(loaded.config.url || loaded.defaultService);
      setModel(loaded.config.model || "");
    });
  }, [load, token]);

  const candidate = (): EmbeddingConfig => ({ kind, url: kind === "service" || kind === "openai" ? url.trim() : undefined, model: kind === "openai" ? model.trim() : undefined });
  const external = !!test?.external || kind === "openai";

  const run = async (what: "test" | "save" | "reembed", action: () => Promise<void>) => {
    setBusy(what);
    setError(null);
    setNotice(null);
    try {
      await action();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      <PageHeader title="Embedding" description="What turns your memories into something searchable by meaning." />
      {needsSignIn ? (
        <Login rejected={token !== ""} onSignedIn={() => void load()} />
      ) : !state ? (
        error ? (
          <Callout.Root color="red" role="alert"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{error}</Callout.Text></Callout.Root>
        ) : (
          <Skeleton height="200px" />
        )
      ) : (
        <Flex direction="column" gap="5">
          <Card size="2">
            <Flex direction="column" gap="1">
              <Text size="2" color="gray">In use right now</Text>
              <Heading size="4" as="h2">{state.enabled ? state.model : "Off (keyword search only)"}</Heading>
              <Text size="2" color="gray">
                {state.status.embedded} of {plural(state.status.total, "memory", "memories")} searchable by meaning
                {state.status.queued > 0 ? `, ${state.status.queued} waiting` : ""}
                {state.status.failed > 0 ? `, ${state.status.failed} gave up` : ""}.
                {state.status.degraded && state.status.lastError ? ` Last problem: ${state.status.lastError}.` : ""}
              </Text>
            </Flex>
          </Card>

          {state.staleVectors > 0 && (
            <Callout.Root color="amber" highContrast role="status">
              <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
              <Callout.Text>
                {plural(state.staleVectors, "memory", "memories")} {state.staleVectors === 1 ? "was" : "were"} made searchable by a different
                setup, so {state.staleVectors === 1 ? "it doesn't" : "they don't"} count for meaning search now. Re-embedding makes them searchable again
                with what is in use.{" "}
                <Button size="1" variant="soft" disabled={busy !== null || !state.enabled} onClick={() => void run("reembed", async () => {
                  const queued = await reembed();
                  setNotice(`${plural(queued, "memory", "memories")} queued. It happens in the background.`);
                  await load();
                })}>Re-embed memories</Button>
              </Callout.Text>
            </Callout.Root>
          )}
          {notice && <Text size="2" color="green">{notice}</Text>}

          <Flex direction="column" gap="3" asChild>
            <section>
              <Heading size="4" as="h2">Choose</Heading>
              <RadioGroup.Root value={kind} onValueChange={(value) => { setKind(value as EmbeddingKind); setTest(null); setConfirm(false); }} aria-label="Where embeddings come from">
                <Flex direction="column" gap="3">
                  {OPTIONS.map((option) => {
                    const unavailable = option.kind === "openai" && !state.keyAvailable;
                    return (
                      <Card key={option.kind} size="2" style={{ opacity: unavailable ? 0.65 : 1 }}>
                        <Text as="label" size="2">
                          <Flex gap="3" align="start">
                            <RadioGroup.Item value={option.kind} disabled={unavailable} aria-label={option.title} />
                            <Flex direction="column" gap="1" style={{ flex: 1 }}>
                              <Text weight="medium">{option.title}</Text>
                              <Text size="2" color="gray">{option.blurb}</Text>
                              {unavailable && (
                                <Text size="1" color="gray">
                                  Not available: this agent was started without an API key for outside services (<Code variant="ghost">-embedder-key-file</Code>).
                                </Text>
                              )}
                              {kind === option.kind && (option.kind === "service" || option.kind === "openai") && (
                                <Flex direction="column" gap="2" mt="2">
                                  <TextField.Root value={url} onChange={(event) => { setUrl(event.target.value); setTest(null); }} placeholder={option.kind === "service" ? state.defaultService : "https://…/v1/embeddings"} aria-label="Service address" spellCheck={false} />
                                  {option.kind === "openai" && (
                                    <TextField.Root value={model} onChange={(event) => { setModel(event.target.value); setTest(null); }} placeholder="Model name, for example text-embedding-3-small" aria-label="Model name" spellCheck={false} />
                                  )}
                                </Flex>
                              )}
                            </Flex>
                          </Flex>
                        </Text>
                      </Card>
                    );
                  })}
                </Flex>
              </RadioGroup.Root>

              {error && (
                <Callout.Root color="red" role="alert">
                  <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
                  <Callout.Text>{error}</Callout.Text>
                </Callout.Root>
              )}

              {test && <TestCard result={test} />}

              {external && (
                <Text as="label" size="2">
                  <Flex gap="2" align="start">
                    <Checkbox checked={confirm} onCheckedChange={(checked) => setConfirm(checked === true)} aria-label="I understand what I remember will be sent to this service" />
                    <span>I understand that what I remember will be sent to this service, outside the cluster.</span>
                  </Flex>
                </Text>
              )}

              <Flex gap="2">
                <Button variant="soft" disabled={busy !== null} onClick={() => void run("test", async () => setTest(await testEmbedding(candidate())))}>
                  {busy === "test" ? "Testing…" : "Test it"}
                </Button>
                <Button disabled={busy !== null || (external && !confirm)} onClick={() => void run("save", async () => {
                  await saveEmbedding(candidate(), confirm);
                  setNotice("Saved. It is in use now.");
                  setTest(null);
                  await load();
                })}>
                  {busy === "save" ? "Saving…" : "Use this"}
                </Button>
              </Flex>
              <Text size="1" color="gray">“Use this” tests first and only switches if it works, so a typo cannot leave you without search.</Text>
            </section>
          </Flex>
        </Flex>
      )}
    </>
  );
}

function TestCard({ result }: { result: TestResult }) {
  if (!result.ok) {
    return (
      <Callout.Root color="red" role="alert">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>It didn't work: {result.error}</Callout.Text>
      </Callout.Root>
    );
  }
  return (
    <Callout.Root color={result.semantic || result.model === "none" ? "green" : "amber"} role="status">
      <Callout.Icon>{result.semantic ? <Check size={16} /> : <CircleAlert size={16} />}</Callout.Icon>
      <Callout.Text>
        {result.model === "none"
          ? "Embedding would be off."
          : <>It works: <Code variant="ghost">{result.model}</Code>, {result.dim} numbers per memory, {result.latencyMs} ms. {result.semantic
            ? `It understands meaning: two sentences about a failing disk scored ${result.related.toFixed(2)}, against ${result.unrelated.toFixed(2)} for an unrelated one.`
            : `It matches words, not meaning: related sentences scored ${result.related.toFixed(2)} against ${result.unrelated.toFixed(2)} for an unrelated one.`}</>}
      </Callout.Text>
    </Callout.Root>
  );
}
