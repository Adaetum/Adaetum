import { AlertDialog, Button, Callout, Card, Checkbox, Flex, Heading, Text } from "@radix-ui/themes";
import { CircleAlert, CircleCheck, CircleDashed, CircleX, LoaderCircle, RotateCw, Square } from "lucide-react";
import { useState } from "react";
import { formatAgo, formatDuration } from "../../lib/format";
import {
  cancelRun,
  describeRecord,
  ProcedureInfo,
  retryRun,
  RUN_STATE,
  RunSnapshot,
  stepsDone,
  stepDisplay,
  stepNote,
  stopReason,
  StepStatus,
  useRunDetail,
} from "../../lib/runsApi";
import { Advanced, AdvancedBlock } from "../Advanced";
import { StatusBadge } from "../StatusBadge";

function StepIcon({ state, tone }: { state: StepStatus["state"]; tone: string }) {
  switch (state) {
    case "done":
      return <CircleCheck size={16} color="var(--green-11)" aria-hidden />;
    case "failed":
      // Stopped for a decision (amber) looks different from broken (red).
      return tone === "amber" ? <CircleAlert size={16} color="var(--amber-11)" aria-hidden /> : <CircleX size={16} color="var(--red-11)" aria-hidden />;
    case "applying":
    case "waiting":
      return <LoaderCircle size={16} color="var(--blue-11)" className="spin" aria-hidden />;
    default:
      return <CircleDashed size={16} color="var(--gray-9)" aria-hidden />;
  }
}

interface RunDetailPanelProps {
  id: string;
  procedures: ProcedureInfo[];
  now: Date;
  onClose: () => void;
  // Called after something changed the run, so the list refreshes at once.
  onChanged: () => void;
}

// RunDetailPanel is one run, live: where it is, what each step is doing and why, and
// the controls that make sense for its state. Retry is always safe because every step
// checks the real system first; the only thing that needs care is approving a step
// that can't be undone, which asks for confirmation naming it.
export function RunDetailPanel({ id, procedures, now, onClose, onChanged }: RunDetailPanelProps) {
  const [nudge, setNudge] = useState(0);
  const { detail, events, error } = useRunDetail(id, nudge);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [approve, setApprove] = useState<string[]>([]);
  const [confirming, setConfirming] = useState(false);

  if (!detail) {
    return (
      <Card size="2">
        {error ? (
          <Callout.Root color="red" role="alert">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{error}</Callout.Text>
          </Callout.Root>
        ) : (
          <Text size="2" color="gray">Loading…</Text>
        )}
      </Card>
    );
  }

  const run: RunSnapshot = detail.run;
  const state = RUN_STATE[run.state];
  const info = procedures.find((p) => p.name === run.procedure);
  const kindOf = (step: string) => info?.steps.find((s) => s.name === step)?.kind;
  const needsApproval = run.state === "needs-human" && run.code === "approval";
  // Steps this run may not yet do. Approved ones are already allowed.
  const waitingOn = needsApproval ? (info?.steps ?? []).filter((s) => s.kind === "destructive" && !(run.approved ?? []).includes(s.name)) : [];
  const chosen = approve.filter((name) => waitingOn.some((s) => s.name === name));
  const reason = stopReason(run);
  const elapsed = (run.finished ? new Date(run.finished) : now).getTime() - new Date(run.started).getTime();

  const act = async (work: () => Promise<void>) => {
    setBusy(true);
    setProblem(null);
    try {
      await work();
      setNudge((n) => n + 1);
      onChanged();
    } catch (err) {
      setProblem(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  };

  const retry = () => act(async () => { await retryRun(run.id, chosen); setApprove([]); });
  const tryAgain = () => (chosen.length > 0 ? setConfirming(true) : void retry());

  return (
    <Card size="2">
      <Flex direction="column" gap="4">
        <Flex justify="between" align="start" gap="3" wrap="wrap">
          <Flex direction="column" gap="1">
            <Heading size="3" as="h3">{run.procedure}</Heading>
            <Text size="2" color="gray">
              Started {formatAgo(run.started, now)}{run.startedBy ? ` by ${run.startedBy}` : ""} ·{" "}
              {run.state === "running" ? "running for" : "took"} {formatDuration(Math.max(0, elapsed / 1000))} ·{" "}
              {stepsDone(run)} of {run.steps.length} steps done{run.attempt > 1 ? ` · attempt ${run.attempt}` : ""}
            </Text>
          </Flex>
          <Flex align="center" gap="2">
            <StatusBadge tone={state.tone}>{state.label}</StatusBadge>
            {run.state === "running" && (
              <Button size="1" variant="soft" color="gray" disabled={busy} onClick={() => void act(() => cancelRun(run.id))}>
                <Square size={12} aria-hidden /> Stop
              </Button>
            )}
            {(run.state === "failed" || run.state === "needs-human" || run.state === "cancelled") && (
              <Button size="1" disabled={busy} onClick={tryAgain}>
                <RotateCw size={12} aria-hidden /> {chosen.length > 0 ? "Approve and try again" : "Try again"}
              </Button>
            )}
            <Button size="1" variant="ghost" color="gray" onClick={onClose}>Close</Button>
          </Flex>
        </Flex>

        {reason && (
          <Callout.Root color={run.state === "needs-human" ? "amber" : run.state === "cancelled" ? "gray" : "red"} role={run.state === "failed" ? "alert" : "status"}>
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>
              {reason}
              {run.state === "failed" && (
                <Text as="div" size="1" mt="1">Trying again starts from the top, and anything already done is left alone.</Text>
              )}
            </Callout.Text>
          </Callout.Root>
        )}

        {waitingOn.length > 0 && (
          <fieldset style={{ border: "none", padding: 0, margin: 0 }}>
            <legend style={{ marginBottom: "var(--space-1)" }}><Text size="2" weight="medium">This run is waiting for your approval to:</Text></legend>
            <Flex direction="column" gap="1">
              {waitingOn.map((step) => (
                <Text as="label" size="2" key={step.name}>
                  <Flex align="center" gap="2">
                    <Checkbox
                      checked={chosen.includes(step.name)}
                      onCheckedChange={(on) => setApprove((current) => on === true ? [...current, step.name] : current.filter((n) => n !== step.name))}
                    />
                    Allow “{step.name}” (can't be undone)
                  </Flex>
                </Text>
              ))}
            </Flex>
          </fieldset>
        )}

        {(problem || error) && (
          <Callout.Root color="red" role="alert">
            <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
            <Callout.Text>{problem ?? error}</Callout.Text>
          </Callout.Root>
        )}

        <ol style={{ listStyle: "none", margin: 0, padding: 0 }} aria-label="Steps">
          {run.steps.map((step) => {
            const status = stepDisplay(step);
            const note = stepNote(step);
            const kind = kindOf(step.name);
            return (
              <li key={step.name} style={{ padding: "6px 0", borderTop: "1px solid var(--gray-a4)" }}>
                <Flex align="start" gap="2">
                  <span style={{ paddingTop: 2 }}><StepIcon state={step.state} tone={status.tone} /></span>
                  <Flex direction="column" gap="1" style={{ minWidth: 0 }}>
                    <Flex align="center" gap="2" wrap="wrap">
                      <Text size="2" weight="medium">{step.name}</Text>
                      <StatusBadge tone={status.tone}>{status.label}</StatusBadge>
                      {kind === "destructive" && <StatusBadge tone="amber">Can't be undone</StatusBadge>}
                      {step.attempts > 1 && <Text size="1" color="gray">tried {step.attempts} times</Text>}
                    </Flex>
                    {note && <Text size="1" color="gray">{note}</Text>}
                  </Flex>
                </Flex>
              </li>
            );
          })}
        </ol>

        <Advanced label="Everything that happened">
          <AdvancedBlock title="Journal" note="Each line is a record the agent wrote before it did the thing, oldest first. It is kept on the host and survives restarts.">
            <table className="sizing-table">
              <thead><tr><th>When</th><th>What</th></tr></thead>
              <tbody>
                {events.map((rec, index) => (
                  <tr key={index}>
                    <td style={{ whiteSpace: "nowrap" }}>{new Date(rec.at).toLocaleTimeString()}</td>
                    <td>{describeRecord(rec)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </AdvancedBlock>
        </Advanced>
      </Flex>

      <AlertDialog.Root open={confirming} onOpenChange={setConfirming}>
        <AlertDialog.Content maxWidth="440px">
          <AlertDialog.Title>Allow steps that can't be undone?</AlertDialog.Title>
          <AlertDialog.Description size="2">
            This run will be allowed to do: {chosen.map((step) => `“${step}”`).join(", ")}. This is recorded under your name.
          </AlertDialog.Description>
          <Flex gap="3" justify="end" mt="4">
            <AlertDialog.Cancel><Button variant="soft" color="gray">Cancel</Button></AlertDialog.Cancel>
            <Button color="red" loading={busy} onClick={() => void retry()}>Approve and try again</Button>
          </Flex>
        </AlertDialog.Content>
      </AlertDialog.Root>
    </Card>
  );
}
