import { Button, Callout, Card, Code, Flex, Heading, Text } from "@radix-ui/themes";
import { CircleAlert, Square } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { cancelRun, RunStatus, useRun } from "../../lib/ansibleApi";
import { StatusBadge } from "../StatusBadge";
import { Tone } from "../../lib/status";

const STATUS: Record<RunStatus, { label: string; tone: Tone }> = {
  running: { label: "Running", tone: "blue" },
  succeeded: { label: "Finished", tone: "green" },
  failed: { label: "Failed", tone: "red" },
  cancelled: { label: "Stopped", tone: "amber" },
  error: { label: "Could not start", tone: "red" },
};

// RunPanel shows one run live: its state and the output, which follows the end
// while the run is going and stops following if you scroll up to read.
export function RunPanel({ id, onClose }: { id: string; onClose: () => void }) {
  const { run, error } = useRun(id);
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const output = useRef<HTMLPreElement>(null);
  const following = useRef(true);

  useEffect(() => {
    const element = output.current;
    if (element && following.current) element.scrollTop = element.scrollHeight;
  }, [run?.lines.length]);

  const stop = async () => {
    setCancelling(true);
    setCancelError(null);
    try {
      await cancelRun(id);
    } catch (err) {
      setCancelError(err instanceof Error ? err.message : String(err));
    } finally {
      setCancelling(false);
    }
  };

  if (!run) {
    return (
      <Card size="2">
        {error ? (
          <Callout.Root color="red" role="alert">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{error}</Callout.Text>
          </Callout.Root>
        ) : (
          <Text size="2" color="gray">Starting…</Text>
        )}
      </Card>
    );
  }

  const status = STATUS[run.status];
  return (
    <Card size="2">
      <Flex direction="column" gap="3">
        <Flex justify="between" align="start" gap="3" wrap="wrap">
          <Flex direction="column" gap="1">
            <Heading size="3" as="h3">{run.title}</Heading>
            <Text size="2" color="gray">
              On <Code variant="ghost">{run.limit.join(", ")}</Code> · {run.check ? "preview (nothing changed)" : "real run"}
            </Text>
          </Flex>
          <Flex align="center" gap="2">
            <StatusBadge tone={status.tone}>{status.label}</StatusBadge>
            {run.status === "running" && (
              <Button size="1" variant="soft" color="gray" onClick={stop} disabled={cancelling}>
                <Square size={12} aria-hidden /> {cancelling ? "Stopping…" : "Stop"}
              </Button>
            )}
            {run.status !== "running" && <Button size="1" variant="ghost" color="gray" onClick={onClose}>Close</Button>}
          </Flex>
        </Flex>

        {run.error && (
          <Callout.Root color="red" role="alert">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{run.error}</Callout.Text>
          </Callout.Root>
        )}
        {cancelError && <Text size="2" color="red">{cancelError}</Text>}
        {run.dropped && <Text size="1" color="gray">The earliest output was dropped to keep this page light.</Text>}

        <pre
          ref={output}
          className="run-output"
          tabIndex={0}
          aria-label="Task output"
          onScroll={(event) => {
            const element = event.currentTarget;
            following.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24;
          }}
        >
          {run.lines.length > 0 ? run.lines.join("\n") : run.status === "running" ? "Waiting for output…" : "No output."}
        </pre>
        {run.status !== "running" && run.exitCode !== undefined && (
          <Text size="1" color="gray">The task finished with code {run.exitCode}.</Text>
        )}
      </Flex>
    </Card>
  );
}
