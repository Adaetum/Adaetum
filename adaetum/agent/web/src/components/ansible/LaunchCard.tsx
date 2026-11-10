import { AlertDialog, Button, Callout, Card, Checkbox, Code, Flex, Select, Switch, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, Play } from "lucide-react";
import { FormEvent, useState } from "react";
import { Capabilities, PlaybookInfo, PlaybookVar, startRun } from "../../lib/ansibleApi";
import { AuthRequiredError, tokenStore } from "../../lib/auth";
import { Login } from "../../pages/Login";

interface LaunchCardProps {
  capabilities: Capabilities;
  // Filled in by a link, for example from the Cluster page's suggested fix.
  initialPlaybook?: string;
  initialLimit?: string;
  initialValues?: Record<string, string>;
  onStarted: (id: string) => void;
}

// LaunchCard is the form for starting a task on demand. It only offers what the
// agent's catalog lists, and the agent checks every value again: this form is a
// convenience, not the safeguard.
//
// Rehearsing is the default, because a real run changes servers as an administrator.
// A real run asks for confirmation naming the servers.
export function LaunchCard({ capabilities, initialPlaybook, initialLimit, initialValues, onStarted }: LaunchCardProps) {
  const first = capabilities.catalog.find((p) => p.id === initialPlaybook) ?? capabilities.catalog[0];
  const [playbookId, setPlaybookId] = useState(first?.id ?? "");
  const [limit, setLimit] = useState(initialLimit ?? "");
  const [rehearse, setRehearse] = useState(true);
  const [values, setValues] = useState<Record<string, string>>(initialValues ?? {});
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  // Launching always needs the access token, even on an agent that shows its pages
  // without one, so the form asks for it when the agent turns the request down.
  const [needsToken, setNeedsToken] = useState(false);

  const playbook = capabilities.catalog.find((p) => p.id === playbookId);

  if (!capabilities.enabled) {
    return (
      <Callout.Root color="gray" role="status">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>{capabilities.reason ?? "This agent cannot start tasks."}</Callout.Text>
      </Callout.Root>
    );
  }
  if (!playbook) return null;
  if (needsToken) {
    return <Login rejected={tokenStore.get() !== ""} onSignedIn={() => setNeedsToken(false)} />;
  }

  const launch = async () => {
    setBusy(true);
    setFailure(null);
    try {
      const id = await startRun({ playbook: playbook.id, limit, check: rehearse, vars: values });
      // The secret has done its job; do not keep it in the form.
      setValues((current) => Object.fromEntries(Object.entries(current).filter(([name]) => playbook.vars.find((v) => v.name === name)?.kind !== "secret")));
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
    if (rehearse) void launch();
    else setConfirming(true);
  };

  return (
    <Card size="2">
      <form onSubmit={submit}>
        <Flex direction="column" gap="4">
          <label>
            <Text as="div" size="2" weight="medium" mb="1">Task</Text>
            <Select.Root value={playbookId} onValueChange={(id) => { setPlaybookId(id); setValues({}); }}>
              <Select.Trigger style={{ width: "100%" }} aria-label="Task" />
              <Select.Content>
                {capabilities.catalog.map((p) => (
                  <Select.Item key={p.id} value={p.id}>{p.title}</Select.Item>
                ))}
              </Select.Content>
            </Select.Root>
            <Text as="div" size="1" color="gray" mt="1">{playbook.description}</Text>
          </label>

          <label>
            <Text as="div" size="2" weight="medium" mb="1">Servers</Text>
            <TextField.Root
              value={limit}
              onChange={(event) => setLimit(event.target.value)}
              placeholder="node-50449, node-56843"
              spellCheck={false}
              autoComplete="off"
              required={playbook.limitRequired}
            />
            <Text as="div" size="1" color="gray" mt="1">
              Exact server names, separated by commas. It never runs on every server by default.
            </Text>
          </label>

          {playbook.vars.map((variable) => (
            <VariableField
              key={`${playbook.id}:${variable.name}`}
              variable={variable}
              value={values[variable.name] ?? variable.default ?? ""}
              onChange={(value) => setValues((current) => ({ ...current, [variable.name]: value }))}
            />
          ))}

          <Text as="label" size="2">
            <Flex gap="2" align="start">
              <Checkbox checked={rehearse} onCheckedChange={(checked) => setRehearse(checked === true)} aria-label="Preview only" />
              <span>
                <strong>Preview only.</strong> Shows what would change, and changes nothing. Untick it to run for real.
              </span>
            </Flex>
          </Text>

          {failure && (
            <Callout.Root color="red" role="alert">
              <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
              <Callout.Text>{failure}</Callout.Text>
            </Callout.Root>
          )}

          <div>
            <Button type="submit" loading={busy} color={rehearse ? undefined : "red"} variant={rehearse ? "solid" : "soft"} highContrast={!rehearse}>
              <Play size={14} aria-hidden /> {busy ? "Starting…" : rehearse ? "Preview" : "Run for real"}
            </Button>
          </div>
        </Flex>
      </form>

      <RealRunConfirmation open={confirming} playbook={playbook} limit={limit} busy={busy} onCancel={() => setConfirming(false)} onConfirm={launch} />
    </Card>
  );
}

function VariableField({ variable, value, onChange }: { variable: PlaybookVar; value: string; onChange: (value: string) => void }) {
  if (variable.kind === "flag") {
    return (
      <Text as="label" size="2">
        <Flex gap="2" align="center">
          <Switch checked={value === "true"} onCheckedChange={(on) => onChange(on ? "true" : "false")} aria-label={variable.label} />
          <span>{variable.label}</span>
        </Flex>
        {variable.help && <Text as="div" size="1" color="gray" mt="1">{variable.help}</Text>}
      </Text>
    );
  }
  if (variable.kind === "choice") {
    return (
      <label>
        <Text as="div" size="2" weight="medium" mb="1">{variable.label}</Text>
        <Select.Root value={value} onValueChange={onChange}>
          <Select.Trigger style={{ width: "100%" }} aria-label={variable.label} />
          <Select.Content>
            {(variable.choices ?? []).map((choice) => <Select.Item key={choice} value={choice}>{choice}</Select.Item>)}
          </Select.Content>
        </Select.Root>
      </label>
    );
  }
  return (
    <label>
      <Text as="div" size="2" weight="medium" mb="1">
        {variable.label}
        {variable.required && <Text color="red"> *</Text>}
      </Text>
      <TextField.Root
        // A secret is masked, never autofilled, and not kept in the form after launch.
        type={variable.kind === "secret" ? "password" : "text"}
        autoComplete="off"
        spellCheck={false}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        required={variable.required}
      />
      {variable.help && <Text as="div" size="1" color="gray" mt="1">{variable.help}</Text>}
    </label>
  );
}

function RealRunConfirmation({ open, playbook, limit, busy, onCancel, onConfirm }: {
  open: boolean;
  playbook: PlaybookInfo;
  limit: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog.Root open={open} onOpenChange={(next) => { if (!next) onCancel(); }}>
      <AlertDialog.Content maxWidth="460px">
        <AlertDialog.Title>Run for real?</AlertDialog.Title>
        <AlertDialog.Description size="2">
          This will run “{playbook.title}” on <Code variant="ghost">{limit.trim() || "no servers"}</Code> as an administrator, one server at
          a time. It is recorded. If you haven't previewed it yet, cancel and tick “Preview only” first.
        </AlertDialog.Description>
        <Flex gap="3" mt="4" justify="end">
          <Button variant="soft" color="gray" onClick={onCancel}>Cancel</Button>
          <Button color="red" highContrast variant="soft" onClick={onConfirm} loading={busy}>Run for real</Button>
        </Flex>
      </AlertDialog.Content>
    </AlertDialog.Root>
  );
}
