import { Button, Callout, Checkbox, Dialog, Flex, SegmentedControl, Select, Spinner, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError, Capabilities, Frequency, Level, Schedule, ScheduleView, saveSchedule } from "../../lib/cleanupApi";
import { WEEKDAYS, describeSchedule, ordinal } from "../../lib/cleanupText";
import { ReadOnlyNotice, TokenField, useToken } from "./shared";

interface ScheduleDialogProps {
  open: boolean;
  onClose: () => void;
  capabilities: Capabilities;
  view: ScheduleView | null;
  onSaved: () => void;
}

const REGISTRY = /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$/;

const FREQUENCIES: { value: Frequency; label: string }[] = [
  { value: "off", label: "Off" },
  { value: "daily", label: "Every day" },
  { value: "weekly", label: "Every week" },
  { value: "monthly", label: "Every month" },
];

function whenText(value: string | undefined): string {
  return value ? new Date(value).toLocaleString([], { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) : "";
}

export function ScheduleDialog({ open, onClose, capabilities, view, onSaved }: ScheduleDialogProps) {
  const [schedule, setSchedule] = useState<Schedule | null>(null);
  const [diskCondition, setDiskCondition] = useState(false);
  const [registriesText, setRegistriesText] = useState("");
  const [token, setToken] = useToken();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Load the saved schedule into the form each time the dialog opens.
  useEffect(() => {
    if (!open || !view) return;
    const saved = view.state.schedule;
    setSchedule(saved);
    setDiskCondition(saved.onlyIfDiskOverPercent > 0);
    setRegistriesText(saved.allowRegistries.join(", "));
    setError(null);
  }, [open, view]);

  if (!schedule) return null;
  const set = (change: Partial<Schedule>) => setSchedule((current) => (current ? { ...current, ...change } : current));
  const editable = capabilities.canApply && Boolean(view?.available);

  const registries = registriesText.split(",").map((value) => value.trim().toLowerCase()).filter(Boolean);
  const badRegistry = registries.find((value) => !REGISTRY.test(value));
  const timeValid = /^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(schedule.time);
  const maxValid = schedule.maxImages >= 1 && schedule.maxImages <= 200;
  const percentValid = !diskCondition || (schedule.onlyIfDiskOverPercent >= 1 && schedule.onlyIfDiskOverPercent <= 99);
  const formValid = (!badRegistry || schedule.level === "aggressive") && timeValid && maxValid && percentValid;

  const payload: Schedule = {
    ...schedule,
    onlyIfDiskOverPercent: diskCondition ? schedule.onlyIfDiskOverPercent : 0,
    // These apply to a careful plan only; an aggressive one ignores them, so they
    // are not sent and cannot linger in a saved schedule.
    includeOrphans: schedule.level === "careful" && schedule.includeOrphans,
    allowRegistries: schedule.level === "careful" ? registries : [],
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      await saveSchedule(payload, token);
      onSaved();
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 401) setToken("");
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setSaving(false);
    }
  };

  const nextRun = view?.state.nextRun;
  const lastRun = view?.state.lastRun;

  return (
    <Dialog.Root open={open} onOpenChange={(next) => !next && onClose()}>
      <Dialog.Content maxWidth="640px" aria-describedby={undefined}>
        <Dialog.Title>Schedule a cleanup</Dialog.Title>
        <Flex direction="column" gap="4">
          {!editable && <ReadOnlyNotice capabilities={capabilities} />}

          <Callout.Root color="amber" size="1" highContrast>
            <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
            <Callout.Text>
              A schedule removes images with nobody watching. It uses the same safety checks as a manual cleanup, removes a limited number
              each time, and records every run in the audit log.
            </Callout.Text>
          </Callout.Root>

          <fieldset disabled={!editable} style={{ border: 0, padding: 0, margin: 0, minInlineSize: 0 }}>
            <Flex direction="column" gap="4">
              <Flex direction="column" gap="1">
                <Text as="label" size="2" weight="medium">How often</Text>
                <Select.Root value={schedule.frequency} onValueChange={(value) => set({ frequency: value as Frequency })}>
                  <Select.Trigger aria-label="How often" />
                  <Select.Content>
                    {FREQUENCIES.map((option) => (
                      <Select.Item key={option.value} value={option.value}>{option.label}</Select.Item>
                    ))}
                  </Select.Content>
                </Select.Root>
              </Flex>

              {schedule.frequency !== "off" && (
                <>
                  <Flex gap="4" wrap="wrap">
                    {schedule.frequency === "weekly" && (
                      <Flex direction="column" gap="1">
                        <Text size="2" weight="medium">On</Text>
                        <Select.Root value={String(schedule.weekday)} onValueChange={(value) => set({ weekday: Number(value) })}>
                          <Select.Trigger aria-label="Day of the week" />
                          <Select.Content>
                            {WEEKDAYS.map((name, index) => (
                              <Select.Item key={name} value={String(index)}>{name}</Select.Item>
                            ))}
                          </Select.Content>
                        </Select.Root>
                      </Flex>
                    )}
                    {schedule.frequency === "monthly" && (
                      <Flex direction="column" gap="1">
                        <Text size="2" weight="medium">On the</Text>
                        <Select.Root value={String(schedule.dayOfMonth)} onValueChange={(value) => set({ dayOfMonth: Number(value) })}>
                          <Select.Trigger aria-label="Day of the month" />
                          <Select.Content>
                            {Array.from({ length: 28 }, (_, index) => index + 1).map((day) => (
                              <Select.Item key={day} value={String(day)}>{ordinal(day)}</Select.Item>
                            ))}
                          </Select.Content>
                        </Select.Root>
                      </Flex>
                    )}
                    <Flex direction="column" gap="1">
                      <Text as="label" size="2" weight="medium" htmlFor="schedule-time">At (server time)</Text>
                      <div style={{ width: 130 }}>
                        <TextField.Root id="schedule-time" type="time" value={schedule.time} onChange={(event) => set({ time: event.target.value })} />
                      </div>
                      <Text size="1" color="gray">Your maintenance window is 03:00 to 06:00.</Text>
                    </Flex>
                  </Flex>

                  <Flex direction="column" gap="2">
                    <Text as="label" size="2">
                      <Flex gap="2" align="center">
                        <Checkbox checked={diskCondition} onCheckedChange={(value) => { setDiskCondition(value === true); if (value === true && schedule.onlyIfDiskOverPercent === 0) set({ onlyIfDiskOverPercent: 80 }); }} />
                        Only when a disk is getting full
                      </Flex>
                    </Text>
                    {diskCondition && (
                      <Flex align="center" gap="2" ml="6">
                        <Text size="2">Run if a disk is over</Text>
                        <div style={{ width: 80 }}>
                          <TextField.Root type="number" min={1} max={99} aria-label="Disk percentage" value={schedule.onlyIfDiskOverPercent} onChange={(event) => set({ onlyIfDiskOverPercent: Number(event.target.value) })} />
                        </div>
                        <Text size="2">% full</Text>
                      </Flex>
                    )}
                  </Flex>

                  <Flex direction="column" gap="1">
                    <Text size="2" weight="medium">What it does</Text>
                    <SegmentedControl.Root value={schedule.checkOnly ? "check" : "remove"} onValueChange={(value) => set({ checkOnly: value === "check" })} aria-label="What the schedule does">
                      <SegmentedControl.Item value="remove">Remove images</SegmentedControl.Item>
                      <SegmentedControl.Item value="check">Check only</SegmentedControl.Item>
                    </SegmentedControl.Root>
                    <Text size="1" color="gray">
                      Check only records what would be removed and deletes nothing. It is a good way to watch a schedule before trusting it.
                    </Text>
                  </Flex>

                  <Flex direction="column" gap="1">
                    <Text size="2" weight="medium">How much</Text>
                    <SegmentedControl.Root value={schedule.level} onValueChange={(value) => set({ level: value as Level })} aria-label="How much the schedule removes">
                      <SegmentedControl.Item value="aggressive">Aggressive</SegmentedControl.Item>
                      <SegmentedControl.Item value="careful">Careful</SegmentedControl.Item>
                    </SegmentedControl.Root>
                    <Text size="1" color="gray">
                      {schedule.level === "aggressive"
                        ? "Removes every image no container on the server is using, including ones another server or a rollback might want. Images in use and system images are never touched."
                        : "Also keeps images the cluster references and images from registries that may not be reachable."}
                    </Text>
                  </Flex>

                  <Flex align="center" gap="2" wrap="wrap">
                    <Text as="label" size="2" htmlFor="schedule-max">Remove at most</Text>
                    <div style={{ width: 88 }}>
                      <TextField.Root id="schedule-max" type="number" min={1} max={200} value={schedule.maxImages} onChange={(event) => set({ maxImages: Number(event.target.value) })} />
                    </div>
                    <Text size="2">images each time</Text>
                  </Flex>

                  {schedule.level === "careful" && (
                  <Flex direction="column" gap="2">
                    <Text as="label" size="2">
                      <Flex gap="2" align="start">
                        <Checkbox checked={schedule.includeOrphans} onCheckedChange={(value) => set({ includeOrphans: value === true })} mt="1" />
                        <span>
                          <Text weight="medium">Also include apps with no version running</Text>
                          <Text as="div" size="1" color="gray">Larger savings, but they are downloaded again if you deploy those apps later.</Text>
                        </span>
                      </Flex>
                    </Text>
                    <Flex direction="column" gap="1">
                      <Text as="label" size="2" weight="medium" htmlFor="schedule-registries">Extra registries to allow (optional)</Text>
                      <TextField.Root id="schedule-registries" placeholder="for example ghcr.io" value={registriesText} onChange={(event) => setRegistriesText(event.target.value)} />
                      <Text size="1" color={badRegistry ? "red" : "gray"}>
                        {badRegistry ? `“${badRegistry}” is not a registry name.` : `Separate with commas. Already allowed: ${capabilities.defaultRegistries.join(", ")}.`}
                      </Text>
                    </Flex>
                  </Flex>
                  )}
                </>
              )}
            </Flex>
          </fieldset>

          <Flex direction="column" gap="1">
            <Text size="2" weight="medium">{describeSchedule(payload)}</Text>
            {nextRun && view?.state.schedule.frequency !== "off" && <Text size="2" color="gray">Next run: {whenText(nextRun)}</Text>}
            {lastRun && <Text size="2" color="gray">Last time: {lastRun.detail} ({whenText(lastRun.at)})</Text>}
          </Flex>

          {editable && <TokenField token={token} onChange={setToken} />}

          {error && (
            <Callout.Root color="red" role="alert" highContrast>
              <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
              <Callout.Text>{error}</Callout.Text>
            </Callout.Root>
          )}

          <Flex justify="end" gap="2">
            <Button variant="soft" color="gray" onClick={onClose}>Close</Button>
            {editable && (
              <Button disabled={!formValid || !token || saving} onClick={save}>
                {saving ? <Spinner size="1" /> : null}
                Save schedule
              </Button>
            )}
          </Flex>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}
