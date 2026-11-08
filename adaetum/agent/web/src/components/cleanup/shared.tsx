import { Callout, Code, Flex, IconButton, Text, TextField } from "@radix-ui/themes";
import { Check, Copy, Info, KeyRound } from "lucide-react";
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { Capabilities, getCapabilities, getHistory, getSchedule, Run, ScheduleView, tokenStore } from "../../lib/cleanupApi";

// useToken exposes the shared access token, so it is asked for once per browser
// session and never persisted beyond it.
export function useToken(): [string, (token: string) => void] {
  const token = useSyncExternalStore(tokenStore.subscribe, tokenStore.get);
  const update = useCallback((next: string) => tokenStore.set(next), []);
  return [token, update];
}

export function TokenField({ token, onChange }: { token: string; onChange: (token: string) => void }) {
  return (
    <Flex direction="column" gap="1">
      <Text as="label" size="2" weight="medium" htmlFor="agent-token">Agent access token</Text>
      <TextField.Root
        id="agent-token"
        type="password"
        autoComplete="off"
        placeholder="Paste the token"
        value={token}
        onChange={(event) => onChange(event.target.value.trim())}
      >
        <TextField.Slot><KeyRound size={16} aria-hidden /></TextField.Slot>
      </TextField.Root>
      <Text size="1" color="gray">
        Changes need it. Show it on the server with <Code variant="ghost">sudo cat /etc/adaetum/agent-token</Code>. It is kept for this browser session only.
      </Text>
    </Flex>
  );
}

// CommandBox shows a command the operator can copy, for when the UI cannot make
// the change itself.
export function CommandBox({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard access can be denied; the text stays selectable */
    }
  };
  return (
    <Flex align="start" gap="2" className="command-box">
      <Code size="2" variant="ghost" style={{ flex: 1, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{command}</Code>
      <IconButton size="1" variant="ghost" color="gray" aria-label="Copy command" onClick={copy}>
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </IconButton>
    </Flex>
  );
}

// ReadOnlyNotice explains, in the agent's own words, why changes are off.
export function ReadOnlyNotice({ capabilities }: { capabilities: Capabilities }) {
  return (
    <Callout.Root color="gray" size="1">
      <Callout.Icon><Info size={16} /></Callout.Icon>
      <Callout.Text>
        <Text as="div" size="2">{capabilities.reason}</Text>
        {capabilities.hint && (
          <Flex direction="column" gap="2" mt="2">
            <Text size="2" color="gray">To clean {capabilities.host} now, run these on that server:</Text>
            <CommandBox command={capabilities.hint} />
          </Flex>
        )}
      </Callout.Text>
    </Callout.Root>
  );
}

export interface CleanupData {
  capabilities: Capabilities | null;
  history: Run[];
  schedule: ScheduleView | null;
  loading: boolean;
  reload: () => void;
}

// useCleanupData loads what the cleanup card shows. If the agent has no cleanup
// endpoints (an older agent), capabilities stay null and the card is not shown.
export function useCleanupData(): CleanupData {
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null);
  const [history, setHistory] = useState<Run[]>([]);
  const [schedule, setSchedule] = useState<ScheduleView | null>(null);
  const [loading, setLoading] = useState(true);
  const [version, setVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    Promise.allSettled([getCapabilities(controller.signal), getHistory(controller.signal), getSchedule(controller.signal)]).then(
      ([caps, runs, sched]) => {
        if (controller.signal.aborted) return;
        setCapabilities(caps.status === "fulfilled" ? caps.value : null);
        setHistory(runs.status === "fulfilled" ? runs.value : []);
        setSchedule(sched.status === "fulfilled" ? sched.value : null);
        setLoading(false);
      },
    );
    return () => controller.abort();
  }, [version]);

  return { capabilities, history, schedule, loading, reload: () => setVersion((value) => value + 1) };
}
