import { Button, Callout, Card, Code, Dialog, Flex, Heading, Select, Skeleton, Switch, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, ShieldCheck } from "lucide-react";
import { FormEvent, useState } from "react";
import { useNow } from "../app/useReport";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { AuthRequiredError, useToken } from "../lib/auth";
import { formatAgo, plural } from "../lib/format";
import { Lever, setLever, useLevers } from "../lib/securityApi";
import { Login } from "./Login";

const DURATIONS = [
  { value: "60", label: "1 hour, then turn off by itself" },
  { value: "240", label: "4 hours, then turn off by itself" },
  { value: "1440", label: "24 hours, then turn off by itself" },
  { value: "0", label: "Until I turn it off" },
];

// Security lists the powerful things the agent can do to servers. They are off
// unless turned on. Flipping one applies to every server at once, takes effect at
// once, and is recorded. The agent decides whether a switch can be turned on at all:
// a missing token or program, or a record that cannot be written, keeps it off no
// matter what is clicked here.
export function Security() {
  const { levers, error, refresh } = useLevers();
  const now = new Date(useNow(15_000));
  const token = useToken();

  const onCount = levers?.filter((lever) => lever.enabled).length ?? 0;
  return (
    <>
      <PageHeader title="Security" description="Powerful actions this agent can take on your servers. They are off unless you turn them on." />
      {error === "This agent needs an access token." ? (
        <Login rejected={token !== ""} onSignedIn={refresh} />
      ) : error && !levers ? (
        <Callout.Root color="red" role="alert">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>Couldn't read the security settings: {error}</Callout.Text>
        </Callout.Root>
      ) : !levers ? (
        <Flex direction="column" gap="4" aria-busy="true" aria-label="Loading">
          <Skeleton height="72px" />
          <Skeleton height="160px" />
        </Flex>
      ) : (
        <Flex direction="column" gap="5">
          {onCount === 0 ? (
            <Callout.Root color="green" highContrast role="status">
              <Callout.Icon><ShieldCheck size={16} /></Callout.Icon>
              <Callout.Text>Every powerful action is off.</Callout.Text>
            </Callout.Root>
          ) : (
            <Callout.Root color="amber" highContrast role="status">
              <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
              <Callout.Text>
                {plural(onCount, "powerful action")} {onCount === 1 ? "is" : "are"} on. Turn {onCount === 1 ? "it" : "them"} off when you are done.
              </Callout.Text>
            </Callout.Root>
          )}
          <Text size="2" color="gray">
            Changes apply to every server at once and take effect immediately. Every change is recorded with when it was made.
          </Text>
          {levers.map((lever) => (
            <LeverCard key={lever.key} lever={lever} now={now} onChanged={refresh} />
          ))}
        </Flex>
      )}
    </>
  );
}

function LeverCard({ lever, now, onChanged }: { lever: Lever; now: Date; onChanged: () => void }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [needsSignIn, setNeedsSignIn] = useState(false);

  const state = lever.enabled
    ? { label: "On", tone: "amber" as const }
    : !lever.available
      ? { label: "Unavailable", tone: "gray" as const }
      : { label: "Off", tone: "green" as const };

  const turnOff = async () => {
    setBusy(true);
    setFailure(null);
    try {
      await setLever(lever.key, false, 0, "");
      onChanged();
    } catch (err) {
      if (err instanceof AuthRequiredError) setNeedsSignIn(true);
      else setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const canSwitch = lever.toggleable && (lever.available || lever.enabled);
  return (
    <Card size="2">
      <Flex direction="column" gap="3">
        <Flex justify="between" align="start" gap="3" wrap="wrap">
          <Flex direction="column" gap="1" style={{ maxWidth: "42rem" }}>
            <Flex align="center" gap="2" wrap="wrap">
              <Heading size="3" as="h2">{lever.title}</Heading>
              <StatusBadge tone={state.tone}>{state.label}</StatusBadge>
              {lever.risk === "highest" && <StatusBadge tone="red">Highest risk</StatusBadge>}
            </Flex>
            <Text size="2" color="gray">{lever.description}</Text>
          </Flex>
          {canSwitch && (
            <Switch
              size="3"
              checked={lever.enabled}
              disabled={busy}
              aria-label={`${lever.title}: ${lever.enabled ? "on" : "off"}`}
              onCheckedChange={(on) => (on ? setAsking(true) : turnOff())}
            />
          )}
        </Flex>

        {lever.enabled && lever.until && (
          <Text size="2">Turns off by itself at {new Date(lever.until).toLocaleString()}.</Text>
        )}
        {!lever.enabled && lever.reason && <Text size="2" color="gray">{lever.reason}</Text>}
        {!lever.toggleable && (
          <Text size="1" color="gray">This agent's setting is fixed when it starts, so it can't be changed from here.</Text>
        )}
        {lever.changedAt && (
          <Text size="1" color="gray">Last changed {formatAgo(lever.changedAt, now)} by {lever.changedBy || "someone"}.</Text>
        )}
        {failure && (
          <Callout.Root color="red" role="alert">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{failure}</Callout.Text>
          </Callout.Root>
        )}
        {needsSignIn && <Login rejected={false} onSignedIn={() => { setNeedsSignIn(false); onChanged(); }} />}
      </Flex>

      <TurnOnDialog lever={lever} open={asking} onClose={() => setAsking(false)} onDone={() => { setAsking(false); onChanged(); }} />
    </Card>
  );
}

// TurnOnDialog is the deliberate part: a time limit, and typing the setting's name.
// The agent checks the same confirmation, so a script cannot skip it.
function TurnOnDialog({ lever, open, onClose, onDone }: { lever: Lever; open: boolean; onClose: () => void; onDone: () => void }) {
  const [duration, setDuration] = useState("60");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await setLever(lever.key, true, Number(duration), confirm);
      setConfirm("");
      onDone();
    } catch (err) {
      setFailure(err instanceof AuthRequiredError ? "Sign in first." : err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog.Root open={open} onOpenChange={(next) => { if (!next) { setConfirm(""); setFailure(null); onClose(); } }}>
      <Dialog.Content maxWidth="480px">
        <form onSubmit={submit}>
          <Dialog.Title>Turn on “{lever.title}”?</Dialog.Title>
          <Dialog.Description size="2" mb="3">
            This applies to every server at once and takes effect immediately. It is recorded. Prefer a time limit, so it cannot be
            forgotten.
          </Dialog.Description>
          <Flex direction="column" gap="3">
            <label>
              <Text as="div" size="2" weight="medium" mb="1">For how long</Text>
              <Select.Root value={duration} onValueChange={setDuration}>
                <Select.Trigger style={{ width: "100%" }} aria-label="For how long" />
                <Select.Content>
                  {DURATIONS.map((option) => <Select.Item key={option.value} value={option.value}>{option.label}</Select.Item>)}
                </Select.Content>
              </Select.Root>
            </label>
            <label>
              <Text as="div" size="2" weight="medium" mb="1">
                To confirm, type <Code variant="ghost">{lever.key}</Code>
              </Text>
              <TextField.Root value={confirm} onChange={(event) => setConfirm(event.target.value)} autoComplete="off" spellCheck={false} />
            </label>
            {failure && (
              <Callout.Root color="red" role="alert">
                <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
                <Callout.Text>{failure}</Callout.Text>
              </Callout.Root>
            )}
          </Flex>
          <Flex gap="3" mt="4" justify="end">
            <Button type="button" variant="soft" color="gray" onClick={onClose}>Cancel</Button>
            <Button type="submit" color="red" variant="soft" highContrast loading={busy} disabled={confirm !== lever.key}>Turn on</Button>
          </Flex>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  );
}
