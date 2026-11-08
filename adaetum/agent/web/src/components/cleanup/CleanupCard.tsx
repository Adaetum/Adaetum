import { Button, Card, Flex, Heading, Table, Text } from "@radix-ui/themes";
import { CalendarClock, Eraser } from "lucide-react";
import { useState } from "react";
import { describeRun, describeSchedule, triggerLabel } from "../../lib/cleanupText";
import { formatAgo } from "../../lib/format";
import { ScrollRegion } from "../ScrollRegion";
import { StatusBadge } from "../StatusBadge";
import { ReviewDialog } from "./ReviewDialog";
import { ScheduleDialog } from "./ScheduleDialog";
import { ReadOnlyNotice, useCleanupData } from "./shared";

interface CleanupCardProps {
  // Container-image disk space per byte the runtime reports; see cleanupText.
  diskRatio: number;
  // Called after a cleanup finishes or a schedule is saved, so the page reloads.
  onChanged: () => void;
}

function when(iso: string): string {
  return new Date(iso).toLocaleString([], { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

// CleanupCard is the "Free up space" control on the Storage page: review and run
// a cleanup, schedule one, and see what has happened before. If the agent has no
// cleanup endpoints, it is not shown.
export function CleanupCard({ diskRatio, onChanged }: CleanupCardProps) {
  const { capabilities, history, schedule, reload } = useCleanupData();
  const [reviewing, setReviewing] = useState(false);
  const [scheduling, setScheduling] = useState(false);
  const [showHistory, setShowHistory] = useState(false);

  if (!capabilities) return null;

  const last = history[0];
  const scheduleState = schedule?.state;
  const scheduled = scheduleState && scheduleState.schedule.frequency !== "off";

  return (
    <>
      <Card size="3">
        <Flex direction="column" gap="4">
          <Flex justify="between" align="start" gap="4" wrap="wrap">
            <Flex direction="column" gap="1" style={{ flex: "1 1 320px" }}>
              <Heading size="4" as="h2">Free up space</Heading>
              <Text size="2" color="gray">
                Remove container images that no container on this server is using. Anything an app needs later is downloaded again.
              </Text>
            </Flex>
            <Flex gap="2" wrap="wrap">
              <Button onClick={() => setReviewing(true)}>
                <Eraser size={16} /> Review and clean…
              </Button>
              <Button variant="soft" onClick={() => setScheduling(true)}>
                <CalendarClock size={16} /> Schedule…
              </Button>
            </Flex>
          </Flex>

          {!capabilities.canApply && <ReadOnlyNotice capabilities={capabilities} />}

          <Flex direction="column" gap="1">
            <Text size="2">
              <Text color="gray">Last cleanup: </Text>
              {last ? `${formatAgo(last.startedAt, new Date())} · ${describeRun(last)} · ${triggerLabel(last.trigger)}` : "none yet"}
            </Text>
            <Text size="2">
              <Text color="gray">Schedule: </Text>
              {scheduled ? describeSchedule(scheduleState.schedule) : "none"}
              {scheduled && scheduleState.nextRun && <Text color="gray"> · next {when(scheduleState.nextRun)}</Text>}
            </Text>
          </Flex>

          {history.length > 0 && (
            <Flex direction="column" gap="2">
              <div>
                <Button variant="ghost" color="gray" size="1" onClick={() => setShowHistory((value) => !value)} aria-expanded={showHistory}>
                  {showHistory ? "Hide history" : `Show history (${history.length})`}
                </Button>
              </div>
              {showHistory && (
                <ScrollRegion label="Cleanup history" wrap>
                  <Table.Root variant="surface" size="1">
                    <Table.Header>
                      <Table.Row>
                        <Table.ColumnHeaderCell>When</Table.ColumnHeaderCell>
                        <Table.ColumnHeaderCell>How</Table.ColumnHeaderCell>
                        <Table.ColumnHeaderCell>Result</Table.ColumnHeaderCell>
                      </Table.Row>
                    </Table.Header>
                    <Table.Body>
                      {history.map((run) => (
                        <Table.Row key={`${run.planId}-${run.startedAt}`}>
                          <Table.Cell style={{ whiteSpace: "nowrap" }}>{when(run.startedAt)}</Table.Cell>
                          <Table.Cell style={{ whiteSpace: "nowrap" }}>
                            <StatusBadge tone={run.trigger === "schedule" ? "blue" : "gray"}>{triggerLabel(run.trigger)}</StatusBadge>
                          </Table.Cell>
                          <Table.Cell>{describeRun(run)}</Table.Cell>
                        </Table.Row>
                      ))}
                    </Table.Body>
                  </Table.Root>
                </ScrollRegion>
              )}
            </Flex>
          )}
        </Flex>
      </Card>

      <ReviewDialog
        open={reviewing}
        onClose={() => setReviewing(false)}
        capabilities={capabilities}
        diskRatio={diskRatio}
        onFinished={() => {
          reload();
          onChanged();
        }}
      />
      <ScheduleDialog
        open={scheduling}
        onClose={() => setScheduling(false)}
        capabilities={capabilities}
        view={schedule}
        onSaved={() => {
          reload();
          onChanged();
        }}
      />
    </>
  );
}
