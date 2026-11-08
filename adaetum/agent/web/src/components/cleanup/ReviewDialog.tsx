import { Badge, Button, Callout, Checkbox, Dialog, Flex, Heading, Progress, SegmentedControl, Spinner, Table, Text, TextField } from "@radix-ui/themes";
import { CircleAlert, CircleCheck, TriangleAlert } from "lucide-react";
import { ReactNode, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  AuditEntry,
  Capabilities,
  CleanupPlan,
  Job,
  Level,
  cancelJob,
  getJob,
  makePlan,
  startApply,
} from "../../lib/cleanupApi";
import { CATEGORY_LABEL, blockedRegistries, estimateDisk, groupItems, groupKept } from "../../lib/cleanupText";
import { formatBytes, plural } from "../../lib/format";
import { ScrollRegion } from "../ScrollRegion";
import { ReadOnlyNotice, TokenField, useToken } from "./shared";

type Step = "review" | "confirm" | "running" | "done";

interface ReviewDialogProps {
  open: boolean;
  onClose: () => void;
  capabilities: Capabilities;
  diskRatio: number;
  // Called after a run finishes so the page can reload its numbers.
  onFinished: () => void;
}

// Bounded, but not tightly: a server with a backlog needs a few runs, not dozens.
const DEFAULT_MAX = 100;
const PROGRESS_EVENTS = new Set(["removed", "would-remove", "skip", "fail"]);

function entryLine(entry: AuditEntry): string {
  const name = entry.names?.[0] ?? entry.image ?? "";
  switch (entry.event) {
    case "remove":
      return `Removing ${name}…`;
    case "removed":
      return `Removed ${name}`;
    case "would-remove":
      return `Would remove ${name}`;
    case "skip":
      return `Skipped ${name}: ${entry.reason}`;
    case "fail":
      return `Failed ${name}: ${entry.reason}`;
    default:
      return "";
  }
}

export function ReviewDialog({ open, onClose, capabilities, diskRatio, onFinished }: ReviewDialogProps) {
  const [step, setStep] = useState<Step>("review");
  const [level, setLevel] = useState<Level>("aggressive");
  const [includeOrphans, setIncludeOrphans] = useState(false);
  const [extraRegistries, setExtraRegistries] = useState<string[]>([]);
  // Registries whose images were kept at some point, so their switches stay put
  // after the operator allows them and the plan no longer lists them as kept.
  const [knownBlocked, setKnownBlocked] = useState<Record<string, { count: number; bytes: number }>>({});
  const [maxImages, setMaxImages] = useState(String(DEFAULT_MAX));

  const [plan, setPlan] = useState<CleanupPlan | null>(null);
  const [planning, setPlanning] = useState(false);
  const [planError, setPlanError] = useState<string | null>(null);

  const [dryRun, setDryRun] = useState(false);
  const [typedHost, setTypedHost] = useState("");
  const [token, setToken] = useToken();
  const [job, setJob] = useState<Job | null>(null);
  const [runError, setRunError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const jobId = useRef<string | null>(null);

  // Start fresh each time the dialog opens.
  useEffect(() => {
    if (!open) return;
    setStep("review");
    setLevel("aggressive");
    setIncludeOrphans(false);
    setExtraRegistries([]);
    setKnownBlocked({});
    setMaxImages(String(DEFAULT_MAX));
    setPlan(null);
    setPlanError(null);
    setTypedHost("");
    setJob(null);
    setRunError(null);
    jobId.current = null;
  }, [open]);

  // Make (and remake) the preview whenever the scope changes.
  const registriesKey = extraRegistries.join(",");
  useEffect(() => {
    if (!open || step !== "review") return;
    const controller = new AbortController();
    setPlanning(true);
    setPlanError(null);
    makePlan(
      {
        level,
        // The orphan and registry choices only apply to a careful plan; an
        // aggressive plan removes everything no container here uses.
        includeOrphans: level === "careful" && includeOrphans,
        // An empty list means "the server's defaults"; extras are added to them.
        allowRegistries: level === "careful" && extraRegistries.length > 0 ? [...capabilities.defaultRegistries, ...extraRegistries] : [],
      },
      controller.signal,
    )
      .then((next) => {
        setPlan(next);
        setKnownBlocked((previous) => {
          const merged = { ...previous };
          for (const blocked of blockedRegistries(next)) merged[blocked.registry] = { count: blocked.count, bytes: blocked.bytes };
          return merged;
        });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setPlanError(error instanceof Error ? error.message : String(error));
      })
      .finally(() => {
        if (!controller.signal.aborted) setPlanning(false);
      });
    return () => controller.abort();
    // registriesKey stands in for extraRegistries so the effect reruns on content, not identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, step, level, includeOrphans, registriesKey, capabilities.defaultRegistries]);

  const limit = Math.max(1, Math.min(200, Number.parseInt(maxImages, 10) || DEFAULT_MAX));
  const items = plan?.items ?? [];
  const batch = items.slice(0, limit);
  const batchBytes = batch.reduce((total, item) => total + item.compressedBytes, 0);
  const groups = useMemo(() => (plan ? groupItems(plan) : []), [plan]);
  const kept = useMemo(() => (plan ? groupKept(plan) : []), [plan]);

  // Poll the running job.
  useEffect(() => {
    if (step !== "running" || !jobId.current) return;
    const id = jobId.current;
    let stopped = false;
    const poll = async () => {
      try {
        const latest = await getJob(id);
        if (stopped) return;
        setJob(latest);
        if (latest.state !== "running") {
          setStep("done");
          onFinished();
        }
      } catch (error) {
        if (!stopped) setRunError(error instanceof Error ? error.message : String(error));
      }
    };
    poll();
    const timer = setInterval(poll, 1000);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
    // onFinished is stable enough for this purpose; re-subscribing each render would restart polling.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  const begin = async () => {
    if (!plan) return;
    setStarting(true);
    setRunError(null);
    try {
      const started = await startApply(plan.id, limit, dryRun, token);
      jobId.current = started.jobId;
      setStep("running");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) setToken("");
      setRunError(error instanceof Error ? error.message : String(error));
    } finally {
      setStarting(false);
    }
  };

  const toggleRegistry = (registry: string, allowed: boolean) =>
    setExtraRegistries((current) => (allowed ? [...current, registry] : current.filter((value) => value !== registry)));

  const locked = step === "running";
  const processed = job ? job.entries.filter((entry) => PROGRESS_EVENTS.has(entry.event)).length : 0;
  const recent = (job?.entries ?? []).filter((entry) => entry.event !== "start" && entry.event !== "end").slice(-8).reverse();
  const confirmReady = Boolean(token) && (dryRun || typedHost.trim() === capabilities.host);

  return (
    <Dialog.Root open={open} onOpenChange={(next) => !next && !locked && onClose()}>
      <Dialog.Content maxWidth="760px" aria-describedby={undefined}>
        <Dialog.Title>Free up space</Dialog.Title>

        {step === "review" && (
          <Flex direction="column" gap="4">
            <Flex direction="column" gap="2">
              <SegmentedControl.Root value={level} onValueChange={(value) => setLevel(value as Level)} aria-label="How much to remove">
                <SegmentedControl.Item value="aggressive">Aggressive</SegmentedControl.Item>
                <SegmentedControl.Item value="careful">Careful</SegmentedControl.Item>
              </SegmentedControl.Root>
              <Text size="2" color="gray">
                {level === "aggressive"
                  ? "Removes every container image no container on this server is using, including ones another server or a rollback might want. Anything needed later is downloaded again. Images in use here and system images are never touched, and each image is checked again right before it is removed."
                  : "Also keeps images the cluster references (on other servers, for rollback, or for apps scaled to zero) and images from registries that may not be reachable."}
              </Text>
            </Flex>

            {!capabilities.canApply && <ReadOnlyNotice capabilities={capabilities} />}

            {level === "careful" && (
              <Flex direction="column" gap="2">
                <Text as="label" size="2">
                  <Flex gap="2" align="start">
                    <Checkbox checked={includeOrphans} onCheckedChange={(value) => setIncludeOrphans(value === true)} mt="1" />
                    <span>
                      <Text weight="medium">Also include apps with no version running</Text>
                      <Text as="div" size="1" color="gray">
                        Images of apps that are not deployed right now. They are downloaded again if you deploy the app later.
                      </Text>
                    </span>
                  </Flex>
                </Text>
                {Object.entries(knownBlocked).map(([registry, info]) => (
                  <Text as="label" size="2" key={registry}>
                    <Flex gap="2" align="start">
                      <Checkbox
                        checked={extraRegistries.includes(registry)}
                        onCheckedChange={(value) => toggleRegistry(registry, value === true)}
                        mt="1"
                      />
                      <span>
                        <Text weight="medium">Also allow images from {registry}</Text>
                        <Text as="div" size="1" color="gray">
                          {plural(info.count, "image")}, {formatBytes(info.bytes)}. Downloading them again may need credentials or a private network.
                        </Text>
                      </span>
                    </Flex>
                  </Text>
                ))}
              </Flex>
            )}

            {plan?.notes?.map((note) => (
              <Callout.Root key={note} color="amber" size="1" highContrast>
                <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
                <Callout.Text>{note}</Callout.Text>
              </Callout.Root>
            ))}

            {planError && (
              <Callout.Root color="red" role="alert" highContrast>
                <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
                <Callout.Text>{planError}</Callout.Text>
              </Callout.Root>
            )}

            <Flex direction="column" gap="3" style={{ opacity: planning ? 0.55 : 1 }} aria-busy={planning}>
              <Flex align="center" gap="2">
                {planning && <Spinner size="2" />}
                <Heading size="4" as="h2">
                  {!plan
                    ? "Checking what can be removed…"
                    : items.length === 0
                      ? "Nothing can be removed safely with these settings"
                      : `${plural(items.length, "image")} can be removed`}
                </Heading>
              </Flex>
              {plan && items.length > 0 && (
                <Text size="2" color="gray">
                  That frees {estimateDisk(items.reduce((total, item) => total + item.compressedBytes, 0), diskRatio)} on this disk (an estimate).
                </Text>
              )}

              {groups.length > 0 && (
                <ScrollRegion label="Images that can be removed" wrap>
                  <Table.Root variant="surface" size="1">
                    <Table.Header>
                      <Table.Row>
                        <Table.ColumnHeaderCell>App</Table.ColumnHeaderCell>
                        <Table.ColumnHeaderCell>Images</Table.ColumnHeaderCell>
                        <Table.ColumnHeaderCell>Disk freed</Table.ColumnHeaderCell>
                        <Table.ColumnHeaderCell>Why it can go</Table.ColumnHeaderCell>
                      </Table.Row>
                    </Table.Header>
                    <Table.Body>
                      {groups.slice(0, 12).map((group) => (
                        <Table.Row key={group.repository}>
                          <Table.RowHeaderCell><Text size="2" className="mono">{group.repository}</Text></Table.RowHeaderCell>
                          <Table.Cell>{group.count}</Table.Cell>
                          <Table.Cell>{estimateDisk(group.bytes, diskRatio)}</Table.Cell>
                          <Table.Cell>
                            <Badge color={CATEGORY_LABEL[group.category].tone} variant="soft" highContrast>
                              {CATEGORY_LABEL[group.category].label}
                            </Badge>
                          </Table.Cell>
                        </Table.Row>
                      ))}
                    </Table.Body>
                  </Table.Root>
                  {groups.length > 12 && <Text size="1" color="gray">…and {plural(groups.length - 12, "more app")}</Text>}
                </ScrollRegion>
              )}
            </Flex>

            {kept.length > 0 && (
              <details>
                <summary><Text size="2" weight="medium">Kept: {plural(plan?.excluded.length ?? 0, "image")} and why</Text></summary>
                <Flex direction="column" gap="2" mt="2">
                  {kept.map((group) => (
                    <Flex key={group.reason} justify="between" gap="4">
                      <Text size="2" color="gray">{group.label}</Text>
                      <Text size="2" style={{ whiteSpace: "nowrap" }}>{group.count} · {formatBytes(group.bytes)}</Text>
                    </Flex>
                  ))}
                </Flex>
              </details>
            )}

            {capabilities.canApply && items.length > 0 && (
              <Flex align="center" gap="3" wrap="wrap">
                <Text as="label" size="2" htmlFor="max-images">Remove at most</Text>
                <div style={{ width: 88 }}>
                  <TextField.Root id="max-images" type="number" min={1} max={200} value={maxImages} onChange={(event) => setMaxImages(event.target.value)} />
                </div>
                <Text size="2" color="gray">
                  images this time{items.length > limit ? `. The other ${items.length - limit} stay for next time` : ""}.
                </Text>
              </Flex>
            )}

            <Flex justify="end" gap="2" wrap="wrap">
              <Button variant="soft" color="gray" onClick={onClose}>Close</Button>
              {capabilities.canApply && (
                <>
                  <Button variant="soft" disabled={planning || items.length === 0} onClick={() => { setDryRun(true); setStep("confirm"); }}>
                    Check only
                  </Button>
                  <Button color="red" variant="soft" highContrast disabled={planning || items.length === 0} onClick={() => { setDryRun(false); setStep("confirm"); }}>
                    Remove {plural(batch.length, "image")}…
                  </Button>
                </>
              )}
            </Flex>
          </Flex>
        )}

        {step === "confirm" && (
          <Flex direction="column" gap="4">
            <Callout.Root color={dryRun ? "blue" : "amber"} highContrast>
              <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
              <Callout.Text>
                {dryRun
                  ? `This runs every check on ${plural(batch.length, "image")} and records what would be removed. Nothing is deleted.`
                  : `This removes up to ${plural(batch.length, "image")} (${estimateDisk(batchBytes, diskRatio)}) from ${capabilities.host}. They are downloaded again from their registry when an app needs them, and Docker Hub limits how often images can be downloaded.${level === "aggressive" ? " This includes images another server or a rollback might want." : ""}`}
              </Callout.Text>
            </Callout.Root>
            {!dryRun && (
              <Flex direction="column" gap="1">
                <Text as="label" size="2" weight="medium" htmlFor="confirm-host">Type {capabilities.host} to confirm</Text>
                <TextField.Root id="confirm-host" value={typedHost} autoComplete="off" onChange={(event) => setTypedHost(event.target.value)} />
              </Flex>
            )}
            <TokenField token={token} onChange={setToken} />
            {runError && (
              <Callout.Root color="red" role="alert" highContrast>
                <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
                <Callout.Text>{runError}</Callout.Text>
              </Callout.Root>
            )}
            <Flex justify="end" gap="2">
              <Button variant="soft" color="gray" onClick={() => setStep("review")}>Back</Button>
              <Button color={dryRun ? undefined : "red"} variant={dryRun ? "solid" : "soft"} highContrast={!dryRun} disabled={!confirmReady || starting} onClick={begin}>
                {starting ? <Spinner size="1" /> : null}
                {dryRun ? "Run the check" : "Remove images"}
              </Button>
            </Flex>
          </Flex>
        )}

        {step === "running" && (
          <Flex direction="column" gap="4">
            <Flex direction="column" gap="2">
              <Heading size="4" as="h2">{dryRun ? "Checking…" : "Removing images…"}</Heading>
              <Progress value={job && job.total > 0 ? Math.min(100, Math.round((processed / job.total) * 100)) : 0} aria-label="Progress" />
              <Text size="2" color="gray">{processed} of {job?.total ?? batch.length} handled</Text>
            </Flex>
            <Flex direction="column" gap="1" aria-live="polite">
              {recent.map((entry, index) => (
                <Text key={`${entry.time}-${index}`} size="1" className="mono" color={entry.event === "fail" ? "red" : "gray"}>{entryLine(entry)}</Text>
              ))}
            </Flex>
            {runError && <Text size="2" color="red">{runError}</Text>}
            <Flex justify="end">
              <Button variant="soft" color="gray" onClick={() => cancelJob(token).catch(() => undefined)}>Stop after the current image</Button>
            </Flex>
          </Flex>
        )}

        {step === "done" && <Done job={job} dryRun={dryRun} diskRatio={diskRatio} onClose={onClose} />}
      </Dialog.Content>
    </Dialog.Root>
  );
}

function Done({ job, dryRun, diskRatio, onClose }: { job: Job | null; dryRun: boolean; diskRatio: number; onClose: () => void }): ReactNode {
  const outcome = job?.outcome;
  const failed = job?.state === "failed";
  return (
    <Flex direction="column" gap="4">
      <Callout.Root color={failed || (outcome?.failed ?? 0) > 0 ? "red" : "green"} role="status" highContrast>
        <Callout.Icon>{failed || (outcome?.failed ?? 0) > 0 ? <CircleAlert size={16} /> : <CircleCheck size={16} />}</Callout.Icon>
        <Callout.Text>
          {failed
            ? `The cleanup could not run: ${job?.error}`
            : outcome
              ? dryRun
                ? `Checked ${plural(outcome.removed + outcome.skipped + outcome.failed, "image")}. ${plural(outcome.removed, "image")} would be removed. Nothing was deleted.`
                : `Removed ${plural(outcome.removed, "image")}, about ${formatBytes(Math.round(outcome.removedCompressedBytes * diskRatio))} of disk. ${outcome.skipped} skipped, ${outcome.failed} failed.`
              : "Finished."}
        </Callout.Text>
      </Callout.Root>
      {outcome?.stopped && <Text size="2" color="gray">It stopped early: {outcome.stopped}.</Text>}
      {!dryRun && outcome && outcome.removed > 0 && (
        <Text size="2" color="gray">The disk keeps freeing for a few minutes while the runtime collects the layers, so the Storage numbers may lag.</Text>
      )}
      <Flex justify="end"><Button onClick={onClose}>Done</Button></Flex>
    </Flex>
  );
}
