import { Badge, Button, Callout, Card, Code, Flex, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, Send, Trash2 } from "lucide-react";
import { FormEvent, useState } from "react";
import { addDestination, removeDestination, sendTest, useNotifications } from "../lib/notificationsApi";

// Where the cluster's alerts go. Alertmanager hands every warning and critical to Apprise, which
// delivers to each address listed here; the cluster's own ntfy is always one of them, so alerts
// can reach a phone running the ntfy app. Anything added here is stored in OpenBao, test-sent
// before it is saved, and recorded (see agent/internal/notify).
export function Notifications() {
  const { state, setState, error, ntfyHost } = useNotifications();
  const [address, setAddress] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  if (state === null) return <Text size="2" color="gray">This agent has no alert settings: it is not the cluster agent, or it cannot reach OpenBao.</Text>;

  const run = async (what: string, action: () => Promise<typeof state>) => {
    setBusy(what);
    setFailure(null);
    try {
      setState(await action());
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };
  const add = async (event: FormEvent) => {
    event.preventDefault();
    await run("add", async () => {
      const next = await addDestination(address.trim());
      setAddress("");
      return next;
    });
  };

  return (
    <Flex direction="column" gap="4" style={{ maxWidth: 640 }}>
      <Card size="3">
        <Flex direction="column" gap="2">
          <Text size="3" weight="medium">Get alerts on your phone</Text>
          <Text size="2" color="gray">
            Every warning and critical alert is published to the cluster's own ntfy, on the topic <Code>adaetum-alerts</Code>. Install the
            ntfy app, add the server{ntfyHost ? <> <Code>https://{ntfyHost}</Code></> : " (its address is not listed in this cluster yet)"}, sign in as <Code>admin</Code> and
            subscribe to the topic. The password is in OpenBao at <Code>secret/apps/observability/ntfy</Code> (field <Code>admin_password</Code>). The
            phone needs to reach that address, which is private to your tailnet.
          </Text>
        </Flex>
      </Card>

      <Card size="3">
        <Flex direction="column" gap="3">
          <Flex justify="between" align="center" gap="3" wrap="wrap">
            <Text size="3" weight="medium">Where alerts are sent</Text>
            <Button variant="soft" disabled={busy !== null || !state} onClick={() => run("test", sendTest)}>
              <Send size={14} aria-hidden /> {busy === "test" ? "Sending…" : "Send a test"}
            </Button>
          </Flex>
          {state === undefined && !error && <Text size="2" color="gray">Loading…</Text>}
          {state?.reason && <Callout.Root color="amber"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{state.reason}</Callout.Text></Callout.Root>}
          {state?.destinations.map((destination) => (
            <Flex key={destination.id} justify="between" align="center" gap="3">
              <Flex gap="2" align="center" wrap="wrap" style={{ minWidth: 0 }}>
                <Code>{destination.label}</Code>
                {destination.builtIn && <Badge color="gray" variant="soft">Cluster ntfy</Badge>}
              </Flex>
              {!destination.builtIn && !destination.custom && state.editable && (
                <Button size="1" variant="ghost" color="red" disabled={busy !== null} aria-label={`Remove ${destination.label}`} onClick={() => run(destination.id, () => removeDestination(destination.id))}>
                  <Trash2 size={14} aria-hidden /> Remove
                </Button>
              )}
            </Flex>
          ))}
          {state?.lastTest && (
            <Text size="2" color={state.lastTest.ok ? "green" : "red"} role="status">
              Last test, {new Date(state.lastTest.at).toLocaleTimeString()}: {state.lastTest.message}
            </Text>
          )}
          {state?.destinations.length ? <Text size="1" color="gray">Changes reach the cluster's alert sender within a couple of minutes.</Text> : null}
        </Flex>
      </Card>

      {state?.editable && (
        <Card size="3">
          <form onSubmit={add}>
            <Flex direction="column" gap="2">
              <Text size="3" weight="medium" as="label" htmlFor="alert-address">Add a place to send alerts</Text>
              <Text size="2" color="gray">
                Paste an Apprise address for email, Telegram, Discord, Slack, Pushover and many more, such as <Code>mailto://user:password@example.com</Code> or{" "}
                <Code>tgram://bottoken/chatid</Code>. A test message is sent first, and the address is saved only if it goes through. It is kept in OpenBao and never shown again.
              </Text>
              <Flex gap="2" wrap="wrap">
                <TextField.Root id="alert-address" style={{ flex: 1, minWidth: 240 }} value={address} onChange={(event) => setAddress(event.target.value)} placeholder="scheme://…" autoComplete="off" spellCheck={false} />
                <Button type="submit" disabled={busy !== null || address.trim() === ""}>{busy === "add" ? "Testing…" : "Test and add"}</Button>
              </Flex>
            </Flex>
          </form>
        </Card>
      )}

      {(failure || error) && <Callout.Root color="red" role="alert"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{failure ?? error}</Callout.Text></Callout.Root>}
    </Flex>
  );
}
