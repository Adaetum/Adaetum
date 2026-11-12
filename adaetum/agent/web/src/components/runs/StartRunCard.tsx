import { AlertDialog, Button, Callout, Card, Checkbox, Flex, Select, Text, TextArea } from "@radix-ui/themes";
import { CircleAlert, Play } from "lucide-react";
import { FormEvent, useState } from "react";
import { AuthRequiredError, tokenStore } from "../../lib/auth";
import { parseInputs, ProcedureInfo, RunCapabilities, startProcedure } from "../../lib/runsApi";
import { Login } from "../../pages/Login";
import { StatusBadge } from "../StatusBadge";

interface StartRunCardProps {
  capabilities: RunCapabilities;
  procedures: ProcedureInfo[];
  onStarted: (id: string) => void;
}

// StartRunCard starts a procedure on demand. It offers only what the agent lists, and
// the agent checks everything again: this form is a convenience, not the safeguard.
//
// Destructive steps are never approved by default. A run that reaches one without an
// approval stops and asks, so leaving the boxes empty is the safe choice.
export function StartRunCard({ capabilities, procedures, onStarted }: StartRunCardProps) {
  const [name, setName] = useState(procedures[0]?.name ?? "");
  const [text, setText] = useState("");
  const [approved, setApproved] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  // Starting always needs the access token, even on an agent that shows its pages
  // without one, so the form asks for it when the agent turns the request down.
  const [needsToken, setNeedsToken] = useState(false);

  if (!capabilities.available || !capabilities.enabled) {
    return (
      <Callout.Root color="gray" role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>
          {capabilities.reason || "This agent cannot start runs."}
          {capabilities.available && " You can still watch and stop the runs below."}
        </Callout.Text>
      </Callout.Root>
    );
  }
  if (procedures.length === 0) {
    return (
      <Callout.Root color="gray" role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>This agent doesn't know any procedures yet, so there is nothing to start.</Callout.Text>
      </Callout.Root>
    );
  }
  if (needsToken) {
    return <Login rejected={tokenStore.get() !== ""} onSignedIn={() => setNeedsToken(false)} />;
  }

  const procedure = procedures.find((p) => p.name === name) ?? procedures[0];
  const destructive = procedure.steps.filter((step) => step.kind === "destructive");
  const allowed = approved.filter((step) => destructive.some((d) => d.name === step));

  const start = async () => {
    const { inputs, error } = parseInputs(text);
    if (error) {
      setFailure(error);
      setConfirming(false);
      return;
    }
    setBusy(true);
    setFailure(null);
    try {
      const id = await startProcedure({ procedure: procedure.name, inputs, approved: allowed });
      setText("");
      setApproved([]);
      onStarted(id);
    } catch (err) {
      if (err instanceof AuthRequiredError) setNeedsToken(true);
      else setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    // Approving something that can't be undone gets a second look.
    if (allowed.length > 0) setConfirming(true);
    else void start();
  };

  return (
    <Card size="2">
      <form onSubmit={submit}>
        <Flex direction="column" gap="4">
          <label>
            <Text as="div" size="2" weight="medium" mb="1">Procedure</Text>
            <Select.Root value={procedure.name} onValueChange={(value) => { setName(value); setApproved([]); }}>
              <Select.Trigger style={{ width: "100%" }} aria-label="Procedure" />
              <Select.Content>
                {procedures.map((p) => <Select.Item key={p.name} value={p.name}>{p.name}</Select.Item>)}
              </Select.Content>
            </Select.Root>
            {procedure.description && <Text as="div" size="1" color="gray" mt="1">{procedure.description}</Text>}
          </label>

          <div>
            <Text as="div" size="2" weight="medium" mb="1">What it does, in order</Text>
            <ol style={{ margin: 0, paddingLeft: "1.25rem" }}>
              {procedure.steps.map((step) => (
                <li key={step.name}>
                  <Flex align="center" gap="2" wrap="wrap">
                    <Text size="2">{step.name}</Text>
                    {step.kind === "destructive" && <StatusBadge tone="amber">Can't be undone</StatusBadge>}
                    {step.kind === "once-only" && <StatusBadge tone="gray">Done once</StatusBadge>}
                  </Flex>
                </li>
              ))}
            </ol>
            <Text as="div" size="1" color="gray" mt="1">
              Every step checks first, so anything already in place is left alone and running it again is safe.
            </Text>
          </div>

          <label>
            <Text as="div" size="2" weight="medium" mb="1">Settings (optional)</Text>
            <TextArea
              value={text}
              onChange={(event) => setText(event.target.value)}
              placeholder={"node=node-1"}
              rows={3}
              spellCheck={false}
              autoComplete="off"
            />
            <Text as="div" size="1" color="gray" mt="1">
              One per line, like <code>name=value</code>. These are recorded as plain text, so never type a password or token here.
            </Text>
          </label>

          {destructive.length > 0 && (
            <fieldset style={{ border: "none", padding: 0, margin: 0 }}>
              <legend style={{ marginBottom: "var(--space-1)" }}><Text size="2" weight="medium">Allow steps that can't be undone</Text></legend>
              <Flex direction="column" gap="1">
                {destructive.map((step) => (
                  <Text as="label" size="2" key={step.name}>
                    <Flex align="center" gap="2">
                      <Checkbox
                        checked={allowed.includes(step.name)}
                        onCheckedChange={(on) => setApproved((current) => on === true ? [...current, step.name] : current.filter((n) => n !== step.name))}
                      />
                      Allow “{step.name}”
                    </Flex>
                  </Text>
                ))}
              </Flex>
              <Text as="div" size="1" color="gray" mt="1">Left unticked, the run stops at that step and asks you.</Text>
            </fieldset>
          )}

          {failure && (
            <Callout.Root color="red" role="alert">
              <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
              <Callout.Text>{failure}</Callout.Text>
            </Callout.Root>
          )}

          <Flex>
            <Button type="submit" loading={busy}><Play size={14} aria-hidden /> Start</Button>
          </Flex>
        </Flex>
      </form>

      <AlertDialog.Root open={confirming} onOpenChange={setConfirming}>
        <AlertDialog.Content maxWidth="440px">
          <AlertDialog.Title>Allow steps that can't be undone?</AlertDialog.Title>
          <AlertDialog.Description size="2">
            This run will be allowed to do: {allowed.map((step) => `“${step}”`).join(", ")}. This is recorded under your name.
          </AlertDialog.Description>
          <Flex gap="3" justify="end" mt="4">
            <AlertDialog.Cancel><Button variant="soft" color="gray">Cancel</Button></AlertDialog.Cancel>
            <Button color="red" onClick={() => void start()} loading={busy}>Start and allow</Button>
          </Flex>
        </AlertDialog.Content>
      </AlertDialog.Root>
    </Card>
  );
}
