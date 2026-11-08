import { Button, Callout, Card, Checkbox, Code, Flex, Heading, RadioGroup, Skeleton, Text, TextField } from "@radix-ui/themes";
import { Check, CircleAlert } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { AuthRequiredError, useToken } from "../lib/auth";
import {
  AssistantSettings,
  AssistantSettingsState,
  AssistantTest,
  fetchAssistantSettings,
  forgetAssistantKey,
  saveAssistantSettings,
  testAssistantSettings,
} from "../lib/assistantSettingsApi";
import { Login } from "./Login";

// AiModel is where you choose which AI service the assistant talks to, and switch it
// on or off. Every choice is tested before it is used. A key typed here is saved in the
// agent's database (never returned by the API), so it survives restarts.
export function AiModel() {
  const token = useToken();
  const [state, setState] = useState<AssistantSettingsState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [needsSignIn, setNeedsSignIn] = useState(false);
  const [provider, setProvider] = useState("anthropic");
  const [url, setUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [test, setTest] = useState<AssistantTest | null>(null);
  const [busy, setBusy] = useState<"test" | "save" | "off" | "key" | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const next = await fetchAssistantSettings();
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

  // Start the form at what is saved, once.
  useEffect(() => {
    void load().then((loaded) => {
      if (!loaded) return;
      setProvider(loaded.settings.provider || "anthropic");
      setUrl(loaded.settings.url || "");
      setModel(loaded.settings.model || "");
    });
  }, [load, token]);

  const preset = state?.presets.find((p) => p.id === provider);
  const candidate = (enabled: boolean): AssistantSettings => ({
    enabled,
    provider,
    url: provider === "openai" ? url.trim() : undefined,
    model: model.trim() || undefined,
  });
  const external = test ? test.external : !!preset && !preset.local;
  const keyHere = state?.keySource ?? "";

  const run = async (what: "test" | "save" | "off" | "key", action: () => Promise<void>) => {
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

  const choose = (id: string) => {
    setProvider(id);
    setTest(null);
    setConfirm(false);
    setApiKey("");
  };

  return (
    <>
      <PageHeader title="AI model" description="Which AI service the assistant talks to. Free ones work for trying things out." />
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
              <Heading size="4" as="h2">
                {state.status.enabled ? `${state.status.model} (${state.status.provider})` : "Off"}
              </Heading>
              <Text size="2" color="gray">
                {state.status.enabled ? `Conversations are sent to ${state.status.endpoint}.` : state.status.reason}
              </Text>
              {state.status.enabled && (
                <Flex mt="2">
                  <Button size="1" variant="soft" color="gray" disabled={busy !== null} onClick={() => void run("off", async () => {
                    await saveAssistantSettings({ ...state.settings, enabled: false }, "", false);
                    setNotice("The assistant is off. Nothing is sent anywhere.");
                    await load();
                  })}>Switch off</Button>
                </Flex>
              )}
            </Flex>
          </Card>
          {notice && <Text size="2" color="green">{notice}</Text>}

          <Flex direction="column" gap="3" asChild>
            <section>
              <Heading size="4" as="h2">Choose</Heading>
              <RadioGroup.Root value={provider} onValueChange={choose} aria-label="Which AI service">
                <Flex direction="column" gap="3">
                  {state.presets.map((option) => (
                    <Card key={option.id} size="2">
                      <Text as="label" size="2">
                        <Flex gap="3" align="start">
                          <RadioGroup.Item value={option.id} aria-label={option.label} />
                          <Flex direction="column" gap="1" style={{ flex: 1 }}>
                            <Text weight="medium">{option.label}{option.free ? " · free to try" : ""}</Text>
                            <Text size="2" color="gray">{option.hint}</Text>
                            {provider === option.id && (
                              <Flex direction="column" gap="2" mt="2">
                                {option.id === "openai" && (
                                  <TextField.Root value={url} onChange={(e) => { setUrl(e.target.value); setTest(null); }} placeholder="https://…/v1" aria-label="Service address" spellCheck={false} />
                                )}
                                <TextField.Root value={model} onChange={(e) => { setModel(e.target.value); setTest(null); }} placeholder={option.id === "anthropic" ? "Model (leave empty for the default)" : "Model name, from the service's own list"} aria-label="Model name" spellCheck={false} />
                                {option.needsKey && (
                                  <>
                                    <TextField.Root type="password" autoComplete="off" value={apiKey} onChange={(e) => { setApiKey(e.target.value); setTest(null); }} placeholder={keyHere ? "API key (leave empty to keep the current one)" : "API key"} aria-label="API key" />
                                    <Text size="1" color="gray">
                                      {keyHere === "entered" && "A key you saved earlier is in use. "}
                                      {keyHere === "startup" && "A key given when the agent started is in use. "}
                                      A key typed here is saved on the agent's server (never shown again), so it survives restarts and is used by everyone signed in.
                                    </Text>
                                  </>
                                )}
                              </Flex>
                            )}
                          </Flex>
                        </Flex>
                      </Text>
                    </Card>
                  ))}
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
                    <Checkbox checked={confirm} onCheckedChange={(checked) => setConfirm(checked === true)} aria-label="I understand conversations will be sent to this service" />
                    <span>
                      I understand that what I say to the assistant, and what it reads from the agent, will be sent to this service outside this machine.
                      {preset?.free && " Free services often keep and learn from what they receive, so use test data only."}
                    </span>
                  </Flex>
                </Text>
              )}

              <Flex gap="2">
                <Button variant="soft" disabled={busy !== null} onClick={() => void run("test", async () => setTest(await testAssistantSettings(candidate(true), apiKey)))}>
                  {busy === "test" ? "Testing…" : "Test it"}
                </Button>
                <Button disabled={busy !== null || (external && !confirm)} onClick={() => void run("save", async () => {
                  await saveAssistantSettings(candidate(true), apiKey, confirm);
                  setNotice("Saved. The assistant is using it now.");
                  setTest(null);
                  setApiKey("");
                  await load();
                })}>
                  {busy === "save" ? "Saving…" : "Use this"}
                </Button>
                {keyHere === "entered" && (
                  <Button variant="outline" color="gray" disabled={busy !== null} onClick={() => void run("key", async () => {
                    await forgetAssistantKey();
                    setNotice("The saved key was deleted.");
                    await load();
                  })}>Delete saved key</Button>
                )}
              </Flex>
              <Text size="1" color="gray">“Use this” tests first and only switches if it works. The assistant asks before it changes anything, whichever service you use.</Text>
            </section>
          </Flex>
        </Flex>
      )}
    </>
  );
}

function TestCard({ result }: { result: AssistantTest }) {
  if (!result.ok) {
    return (
      <Callout.Root color="red" role="alert">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>It didn't work: {result.error}</Callout.Text>
      </Callout.Root>
    );
  }
  return (
    <Callout.Root color="green" role="status">
      <Callout.Icon><Check size={16} /></Callout.Icon>
      <Callout.Text>
        It works: <Code variant="ghost">{result.model}</Code> on {result.endpoint} answered in {result.latencyMs} ms
        {result.reply ? <> (it said “{result.reply}”)</> : null}.
      </Callout.Text>
    </Callout.Root>
  );
}
