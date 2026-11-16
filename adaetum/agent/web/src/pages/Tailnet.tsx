import { Button, Callout, Card, Dialog, Flex, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, Info } from "lucide-react";
import { FormEvent, useState } from "react";
import { StatusBadge } from "../components/StatusBadge";
import { AuthRequiredError } from "../lib/auth";
import { setTailnetHttps, useTailnetHttps } from "../lib/tailnetApi";

// Tailnet is the switch for Tailscale HTTPS certificates. Every private link in the cluster is
// served through Tailscale, and the Tailscale operator will not give a link an address until
// the tailnet has HTTPS on; the links just spin until it does. It is a tailnet-wide setting in
// the Tailscale account, so changing it needs a Tailscale token. That token is typed each time,
// used once and never kept (see agent/internal/tailnet).
export function Tailnet() {
  const { value, error, refresh } = useTailnetHttps();
  const [asking, setAsking] = useState<boolean | null>(null); // the position being asked for

  if (value === null) return <Text size="2" color="gray">This agent is not running in a cluster, so there are no Tailscale links to turn on.</Text>;
  const on = value?.state === "on";
  const badge = !value ? null : value.state === "on" ? <StatusBadge tone="green">On</StatusBadge>
    : value.state === "off" ? <StatusBadge tone="amber">Off</StatusBadge> : <StatusBadge tone="gray">Not in use</StatusBadge>;
  return (
    <Flex direction="column" gap="4" style={{ maxWidth: 640 }}>
      <Card size="3">
        <Flex direction="column" gap="3">
          <Flex justify="between" align="start" gap="4" wrap="wrap">
            <Flex direction="column" gap="1" style={{ maxWidth: 400 }}>
              <Flex gap="2" align="center"><Text size="3" weight="medium">Tailscale HTTPS</Text>{badge}</Flex>
              <Text size="2" color="gray">
                The cluster's private links (this agent, Argo CD, Rancher, Gitea and the rest) are served over Tailscale HTTPS, and they
                do not come up until it is on for your tailnet. It is a tailnet-wide setting, and it lists your machine names in public
                certificate-transparency logs.
              </Text>
            </Flex>
            {value && value.state !== "unknown" && (
              <Button color={on ? "red" : undefined} variant={on ? "soft" : "solid"} onClick={() => setAsking(!on)}>{on ? "Turn off" : "Turn on"}</Button>
            )}
          </Flex>
          {value?.state === "off" && (
            <Text size="2" color="gray">{value.waiting} of {value.total} links are still waiting for an address.</Text>
          )}
          {value?.state === "unknown" && <Text size="2" color="gray">There are no Tailscale links in this cluster, so there is nothing to turn on.</Text>}
          {error && !value && <Callout.Root color="red" role="alert"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{error}</Callout.Text></Callout.Root>}
        </Flex>
      </Card>
      <ChangeDialog target={asking} onClose={() => setAsking(null)} onDone={() => { setAsking(null); refresh(); }} />
    </Flex>
  );
}

function ChangeDialog({ target, onClose, onDone }: { target: boolean | null; onClose: () => void; onDone: () => void }) {
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const close = () => { setToken(""); setFailure(null); onClose(); };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (target === null) return;
    setBusy(true);
    setFailure(null);
    try {
      await setTailnetHttps(target, token);
      setToken("");
      onDone();
    } catch (err) {
      setFailure(err instanceof AuthRequiredError ? "Sign in first." : err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog.Root open={target !== null} onOpenChange={(next) => { if (!next) close(); }}>
      <Dialog.Content maxWidth="480px">
        <form onSubmit={submit}>
          <Dialog.Title>{target ? "Turn on Tailscale HTTPS?" : "Turn off Tailscale HTTPS?"}</Dialog.Title>
          <Dialog.Description size="2" mb="3">
            {target
              ? "This changes your whole tailnet and lists your machine names in public certificate-transparency logs. The links get their addresses a few minutes after it is on."
              : "This changes your whole tailnet. Every private link in the cluster stops working until it is turned back on."}
          </Dialog.Description>
          <Flex direction="column" gap="3">
            <label>
              <Text as="div" size="2" weight="medium" mb="1">Tailscale access token</Text>
              <TextField.Root type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" spellCheck={false} placeholder="tskey-api-…" />
              <Text as="div" size="1" color="gray" mt="1">
                One that may change tailnet settings: an admin's access token, or an OAuth client's with the feature settings scope. It is
                used for this one change and not kept.
              </Text>
            </label>
            {failure && <Callout.Root color="red" role="alert"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{failure}</Callout.Text></Callout.Root>}
            <Callout.Root color="gray" size="1"><Callout.Icon><Info size={14} /></Callout.Icon><Callout.Text>The change is recorded with your name.</Callout.Text></Callout.Root>
          </Flex>
          <Flex gap="3" mt="4" justify="end">
            <Button type="button" variant="soft" color="gray" onClick={close}>Cancel</Button>
            <Button type="submit" color={target ? undefined : "red"} loading={busy} disabled={token.trim() === ""}>{target ? "Turn on" : "Turn off"}</Button>
          </Flex>
        </form>
      </Dialog.Content>
    </Dialog.Root>
  );
}
