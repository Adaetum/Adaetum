import { Button, Callout, Card, Code, Flex, Select, Tabs, Text, TextField } from "@radix-ui/themes";
import { CircleAlert } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { AccountChoice, BackupVolume, BackupsState, CloudflareAccount, Schedule, StorageInfo, StorageRequest, StorageResult, chooseStorage, getDefaultRule, saveSchedules, setDefaultRule, setVolumeMode, useBackups } from "../lib/backupsApi";
import { formatBytes } from "../lib/format";

// The Backups page. A volume is protected in one of two different ways, and each way has its own
// tab holding its own settings (where it copies to, and when). The Volumes tab only chooses which
// way each volume uses. Longhorn does the scheduled backups; the VolSync pod does sync. Replicas
// keep a volume alive when a disk or machine dies; they are not a backup, and deleting a volume
// deletes every replica with it.
const HEADLINE: Record<BackupsState["summary"], { tone: "green" | "amber" | "red" | "gray"; label: string; text: string }> = {
  off: { tone: "gray", label: "Off", text: "No off-site backup target is set, so nothing here is backed up by a schedule. Replicas protect against a lost disk or machine, not against a deleted volume or a lost cluster." },
  failing: { tone: "red", label: "Target unreachable", text: "A backup target is set but Longhorn cannot reach it, so no new backup can complete." },
  attention: { tone: "amber", label: "Behind", text: "At least one volume has a backup job but no recent backup." },
  unprotected: { tone: "amber", label: "Gaps", text: "Backups run, but some volumes are not on any schedule or sync." },
  ok: { tone: "green", label: "Protected", text: "Every volume has a backup job and a recent backup." },
};

const VOLUME_TONE = { protected: "green", stale: "amber", unprotected: "gray" } as const;
const VOLUME_LABEL = { protected: "Protected", stale: "Behind", unprotected: "Not backed up" } as const;
const PROVIDER_LABEL: Record<string, string> = { r2: "Cloudflare R2", s3: "S3-compatible", azure: "Azure Blob Storage" };

function age(iso?: string): string {
  if (!iso) return "never";
  const hours = (Date.now() - new Date(iso).getTime()) / 3_600_000;
  if (hours < 1) return "under an hour ago";
  if (hours < 48) return `${Math.round(hours)} hours ago`;
  return `${Math.round(hours / 24)} days ago`;
}

// A wide table scrolls sideways inside its card on a narrow screen instead of being cut off.
function Scroll({ children }: { children: React.ReactNode }) {
  return <div style={{ overflowX: "auto" }}>{children}</div>;
}

function Problem({ message }: { message: string }) {
  return <Callout.Root color="red" role="alert"><Callout.Icon><CircleAlert size={16} /></Callout.Icon><Callout.Text>{message}</Callout.Text></Callout.Root>;
}

export function Backups() {
  const { state, error, refresh } = useBackups();
  const header = <PageHeader title="Backups" description="How each volume is protected, and the settings for each way." />;
  if (state === null) return <>{header}<Text size="2" color="gray">This agent cannot read the cluster's storage, so there is nothing to show here.</Text></>;
  if (state === undefined) return <>{header}{error ? <Problem message={error} /> : <Text size="2" color="gray">Loading…</Text>}</>;
  const headline = HEADLINE[state.summary];
  return (
    <>
      {header}
      <Flex direction="column" gap="4" style={{ maxWidth: 860 }}>
        <Card size="3">
          <Flex direction="column" gap="2">
            <Flex gap="2" align="center"><Text size="3" weight="medium">Status</Text><StatusBadge tone={headline.tone}>{headline.label}</StatusBadge></Flex>
            <Text size="2" color="gray">{headline.text}</Text>
            {state.target.configured && <Text size="2">Scheduled target: <Code>{state.target.url}</Code>{state.target.message && !state.target.available ? ` (${state.target.message})` : ""}</Text>}
          </Flex>
        </Card>
        <Tabs.Root defaultValue="volumes">
          <Tabs.List mb="3">
            <Tabs.Trigger value="volumes">Volumes</Tabs.Trigger>
            <Tabs.Trigger value="scheduled">Scheduled backups</Tabs.Trigger>
            <Tabs.Trigger value="sync">Sync</Tabs.Trigger>
          </Tabs.List>
          <Tabs.Content value="volumes"><VolumesTab state={state} refresh={refresh} /></Tabs.Content>
          <Tabs.Content value="scheduled"><ScheduledTab state={state} refresh={refresh} /></Tabs.Content>
          <Tabs.Content value="sync"><SyncTab state={state} refresh={refresh} /></Tabs.Content>
        </Tabs.Root>
      </Flex>
    </>
  );
}

// The two ways to protect a volume are different kinds of thing, so say so where the choice is made.
function ModeExplainer() {
  const side = (title: string, text: string) => (
    <Flex direction="column" gap="1" style={{ flex: "1 1 280px", borderLeft: "3px solid var(--accent-8)", paddingLeft: 12 }}>
      <Text size="2" weight="bold">{title}</Text>
      <Text size="2" color="gray">{text}</Text>
    </Flex>
  );
  return (
    <Flex gap="5" wrap="wrap" mb="3">
      {side("Scheduled: a job on a timer", "On a schedule you set (such as daily), a job takes a snapshot and backs it up. Only the newest few are kept, and each new one pushes the oldest out. Nothing extra runs in the cluster. You can lose up to one interval of changes.")}
      {side("Sync: a stream, with its own pod", "A separate pod (VolSync) keeps copying the volume every 15 minutes, encrypted first. Choosing it deploys that pod by pull request. You can lose about 15 minutes of changes.")}
    </Flex>
  );
}

// Used is what a backup actually copies; the maximum is what the volume may grow to.
function Sizes({ volume }: { volume: BackupVolume }) {
  return <span style={{ whiteSpace: "nowrap" }}>{formatBytes(volume.usedBytes)} <Text size="1" color="gray">of {formatBytes(volume.sizeBytes)}</Text></span>;
}

function claimName(volume: BackupVolume): string {
  return volume.namespace ? `${volume.namespace}/${volume.claim ?? volume.name}` : volume.name;
}

// The choices for one volume: off, each named schedule, or sync.
function ModeOptions({ schedules }: { schedules: Schedule[] }) {
  return (
    <Select.Content>
      <Select.Item value="off">Not backed up</Select.Item>
      {schedules.map((schedule) => <Select.Item key={schedule.name} value={`schedule:${schedule.name}`}>Scheduled: {schedule.name} ({schedule.cron})</Select.Item>)}
      <Select.Item value="sync">Sync: stream via pod</Select.Item>
    </Select.Content>
  );
}

function VolumesTab({ state, refresh }: { state: BackupsState; refresh: () => void }) {
  return (
    <Card size="3">
      <Flex direction="column" gap="2">
        <ModeExplainer />
        <NewVolumeRule schedules={state.schedules} />
        {state.volumes.length === 0 ? <Text size="2" color="gray">There are no volumes.</Text> : (
          <Scroll><table className="sizing-table">
            <thead><tr><th>Volume</th><th>Used / max</th><th>Mode</th><th>Status</th><th>Last copy</th></tr></thead>
            <tbody>{state.volumes.map((volume) => <VolumeRow key={volume.name} volume={volume} schedules={state.schedules} onChanged={refresh} />)}</tbody>
          </table></Scroll>
        )}
      </Flex>
    </Card>
  );
}

function VolumeRow({ volume, schedules, onChanged }: { volume: BackupVolume; schedules: Schedule[]; onChanged: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const [proposal, setProposal] = useState<string | null>(null);
  // A volume mid-change (both modes at once) shows where it is going: sync.
  const current = volume.sync ? "sync" : volume.schedule ? `schedule:${volume.schedule}` : "off";
  async function choose(mode: string) {
    setError(null);
    try {
      const result = await setVolumeMode(volume.name, mode);
      setProposal(result.proposalUrl ?? null);
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }
  return (
    <tr>
      <td>{claimName(volume)}</td>
      <td><Sizes volume={volume} /></td>
      <td>
        <Select.Root size="1" value={current} onValueChange={choose}>
          <Select.Trigger aria-label={`Backup mode for ${volume.claim ?? volume.name}`} />
          <ModeOptions schedules={schedules} />
        </Select.Root>
        {proposal && <Text as="div" size="1"><a href={proposal} target="_blank" rel="noreferrer">Review the pull request</a> to finish this change.</Text>}
        {volume.mode === "both" && <Text as="div" size="1" color="gray">Changing mode: waiting for the pull request to be merged.</Text>}
        {error && <Text as="div" size="1" color="red">{error}</Text>}
      </td>
      <td><StatusBadge tone={VOLUME_TONE[volume.state]}>{VOLUME_LABEL[volume.state]}</StatusBadge></td>
      <td style={{ whiteSpace: "nowrap" }}>{volume.state === "unprotected" ? "—" : age(volume.sync ? volume.lastSyncAt : volume.lastBackupAt)}</td>
    </tr>
  );
}

// What happens to a volume created later. It never changes volumes that already exist, and a
// volume you chose a mode for keeps it.
function NewVolumeRule({ schedules }: { schedules: Schedule[] }) {
  const [mode, setMode] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { getDefaultRule().then((rule) => setMode(rule?.mode ?? null)); }, []);
  if (mode === null) return null;
  async function choose(next: string) {
    setError(null);
    try {
      setMode((await setDefaultRule(next)).mode);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }
  return (
    <Flex direction="column" gap="1" mb="2">
      <Flex gap="2" align="center" wrap="wrap">
        <Text size="2">New volumes added from now on:</Text>
        <Select.Root size="1" value={mode} onValueChange={choose}>
          <Select.Trigger aria-label="Backup mode for new volumes" />
          <Select.Content>
            <Select.Item value="off">Not backed up</Select.Item>
            {schedules.map((schedule) => <Select.Item key={schedule.name} value={`schedule:${schedule.name}`}>Scheduled: {schedule.name} ({schedule.cron})</Select.Item>)}
            {/* A rule naming a schedule that no longer exists does nothing; say so rather than show a blank. */}
            {mode.startsWith("schedule:") && !schedules.some((schedule) => `schedule:${schedule.name}` === mode) && <Select.Item value={mode}>{mode.slice(9)} (no longer exists)</Select.Item>}
          </Select.Content>
        </Select.Root>
      </Flex>
      <Text size="1" color="gray">Applies within a minute of a volume appearing. Volumes that already exist, and any you have chosen a mode for, are left as they are. Sync is chosen per volume.</Text>
      {error && <Text size="1" color="red">{error}</Text>}
    </Flex>
  );
}

// ---- Scheduled backups ----

function ScheduledTab({ state, refresh }: { state: BackupsState; refresh: () => void }) {
  const info = state.storage?.scheduled;
  const chosen = state.volumes.filter((volume) => volume.schedule);
  return (
    <Flex direction="column" gap="3">
      <Text size="2" color="gray">A job on a timer: on each schedule below it takes a snapshot, backs it up to the storage here, keeps the newest few and lets older ones rotate out. No extra pod runs. Put volumes on a schedule on the Volumes tab.</Text>
      <Flex gap="3" wrap="wrap">
        <Facts items={[["Volumes on a schedule", String(chosen.length)], ["In use now", formatBytes(chosen.reduce((sum, volume) => sum + volume.usedBytes, 0))], ["Schedules", String(state.schedules.length)]]} />
      </Flex>
      <StorageCard tier="scheduled" info={info} scheduledInfo={info} onDone={refresh} />
      <SchedulesCard schedules={state.schedules} ready={state.target.configured} onDone={refresh} />
    </Flex>
  );
}

// A row of small labelled figures.
function Facts({ items }: { items: [string, string][] }) {
  return (
    <>
      {items.map(([label, value]) => (
        <Card key={label} style={{ flex: "1 1 150px" }}>
          <Text as="div" size="1" color="gray">{label}</Text>
          <Text as="div" size="4" weight="medium">{value}</Text>
        </Card>
      ))}
    </>
  );
}

// The named schedules. Saving opens a pull request, because they are declared in the repository.
function SchedulesCard({ schedules, ready, onDone }: { schedules: Schedule[]; ready: boolean; onDone: () => void }) {
  const [rows, setRows] = useState<Schedule[]>(schedules);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [proposal, setProposal] = useState<string | null>(null);
  useEffect(() => setRows(schedules), [schedules]);
  const changed = JSON.stringify(rows.map(({ name, cron, retain }) => ({ name, cron, retain }))) !== JSON.stringify(schedules.map(({ name, cron, retain }) => ({ name, cron, retain })));
  const edit = (index: number, patch: Partial<Schedule>) => setRows(rows.map((row, at) => (at === index ? { ...row, ...patch } : row)));
  async function save() {
    setBusy(true);
    setError(null);
    try {
      setProposal((await saveSchedules(rows)).proposalUrl);
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card size="3">
      <Flex direction="column" gap="2">
        <Text size="3" weight="medium">Schedules</Text>
        <Text size="2" color="gray">Each schedule is a name, a time in cron form (minute hour day month weekday, such as <Code>0 3 * * *</Code> for 03:00 every day) and how many backups to keep. Each one becomes a choice for a volume on the Volumes tab.</Text>
        {!ready && <Text size="2" color="gray">Choose storage above and merge its pull request first; the first schedule comes with it.</Text>}
        {ready && (
          <>
            <Scroll><table className="sizing-table">
              <thead><tr><th>Name</th><th>When (cron)</th><th>Keep</th><th>Volumes</th><th /></tr></thead>
              <tbody>
                {rows.map((row, index) => (
                  <tr key={index}>
                    <td><TextField.Root size="1" value={row.name} onChange={(event) => edit(index, { name: event.target.value })} aria-label="Schedule name" style={{ width: 110 }} /></td>
                    <td><TextField.Root size="1" value={row.cron} onChange={(event) => edit(index, { cron: event.target.value })} aria-label="Cron time" style={{ width: 150 }} /></td>
                    <td><TextField.Root size="1" type="number" min={1} max={90} value={String(row.retain)} onChange={(event) => edit(index, { retain: Number(event.target.value) })} aria-label="Backups to keep" style={{ width: 70 }} /></td>
                    <td>{row.volumes ?? 0}</td>
                    <td><Button size="1" variant="soft" color="gray" disabled={rows.length === 1 || (row.volumes ?? 0) > 0} onClick={() => setRows(rows.filter((_, at) => at !== index))}>Remove</Button></td>
                  </tr>
                ))}
              </tbody>
            </table></Scroll>
            <Flex gap="2" align="center">
              <Button size="1" variant="soft" onClick={() => setRows([...rows, { name: "", cron: "0 4 * * 0", retain: 4 }])} disabled={rows.length >= 8}>Add a schedule</Button>
              <Button size="1" loading={busy} disabled={!changed} onClick={save}>Save as a pull request</Button>
            </Flex>
            {error && <Problem message={error} />}
            {proposal && <Text size="2">Review and merge <a href={proposal} target="_blank" rel="noreferrer">the pull request</a> to apply it. The table shows what is merged.</Text>}
          </>
        )}
      </Flex>
    </Card>
  );
}

// ---- Sync ----

function SyncTab({ state, refresh }: { state: BackupsState; refresh: () => void }) {
  const synced = state.volumes.filter((volume) => volume.sync);
  const pod = !state.syncInstalled ? { tone: "gray" as const, label: "Not deployed", text: "No volume is on sync, so the sync pod does not exist. Putting the first volume on sync deploys it by pull request." }
    : state.sync.desired > 0 && state.sync.ready >= state.sync.desired ? { tone: "green" as const, label: "Running", text: "The sync pod is running and copying the volumes below." }
    : { tone: "amber" as const, label: "Starting", text: "VolSync is installed but its pod is not ready yet." };
  return (
    <Flex direction="column" gap="3">
      <Text size="2" color="gray">A stream rather than a job: a separate pod (VolSync) keeps copying each volume on sync every 15 minutes, encrypted before it leaves the cluster. It is deployed by pull request when the first volume uses sync and removed when none do.</Text>
      <Card size="3">
        <Flex direction="column" gap="3">
          <Flex gap="2" align="center"><Text size="3" weight="medium">VolSync pod</Text><StatusBadge tone={pod.tone}>{pod.label}</StatusBadge></Flex>
          <Text size="2" color="gray">{pod.text}</Text>
          <Flex gap="3" wrap="wrap">
            <Facts items={[["Pod replicas ready", state.syncInstalled ? `${state.sync.ready} of ${state.sync.desired}` : "—"], ["Copies every", "15 minutes"], ["Volumes on sync", String(synced.length)], ["In use now", formatBytes(synced.reduce((sum, volume) => sum + volume.usedBytes, 0))]]} />
          </Flex>
        </Flex>
      </Card>
      <StorageCard tier="sync" info={state.storage?.sync} scheduledInfo={state.storage?.scheduled} onDone={refresh} />
      <Card size="3">
        <Flex direction="column" gap="2">
          <Text size="3" weight="medium">Volumes on sync</Text>
          {synced.length === 0 ? <Text size="2" color="gray">No volume uses sync. Choose it on the Volumes tab.</Text> : (
            <Scroll><table className="sizing-table">
              <thead><tr><th>Volume</th><th>Used / max</th><th>Last sync</th></tr></thead>
              <tbody>{synced.map((volume) => <tr key={volume.name}><td>{claimName(volume)}</td><td><Sizes volume={volume} /></td><td>{age(volume.lastSyncAt)}</td></tr>)}</tbody>
            </table></Scroll>
          )}
          <Text size="1" color="gray">The copy interval and retention are declared per volume in the cluster repository (pods/volsync). Editing them from this page is not built yet.</Text>
        </Flex>
      </Card>
    </Flex>
  );
}

// ---- Storage (each tier has its own) ----

// Where a tier copies to. The two tiers are set up separately, so one can use Cloudflare R2 and
// the other AWS, Google or Azure. Credentials go to OpenBao and are not shown again; to change
// them, remove the secret in OpenBao and choose again.
function StorageCard({ tier, info, scheduledInfo, onDone }: { tier: "scheduled" | "sync"; info?: StorageInfo; scheduledInfo?: StorageInfo; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<StorageRequest>({ tier, provider: "r2" });
  const [accounts, setAccounts] = useState<CloudflareAccount[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<StorageResult | null>(null);
  const set = (patch: Partial<StorageRequest>) => setForm({ ...form, ...patch });
  const canReuse = tier === "sync" && scheduledInfo?.configured;

  if (info?.configured && !done) {
    return (
      <Card size="3">
        <Flex direction="column" gap="1">
          <Flex gap="2" align="center"><Text size="3" weight="medium">Storage</Text><StatusBadge tone="green">Set up</StatusBadge></Flex>
          <Text size="2">{PROVIDER_LABEL[info.provider ?? ""] ?? info.provider}: <Code>{info.bucket}</Code></Text>
          <Text size="1" color="gray">The credentials are in OpenBao and are not shown. To change them, remove the secret there and choose storage again.</Text>
        </Flex>
      </Card>
    );
  }
  if (done) {
    return (
      <Callout.Root color="green"><Callout.Text>
        {tier === "scheduled"
          ? <>Storage <Code>{done.bucket}</Code> is ready and its credentials are in OpenBao. Review and merge <a href={done.proposalUrl} target="_blank" rel="noreferrer">the pull request</a> to turn scheduled backups on.</>
          : <>Sync storage <Code>{done.bucket}</Code> is ready. Choose Sync for a volume on the Volumes tab.</>}
      </Callout.Text></Callout.Root>
    );
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      setDone(await chooseStorage(form.same ? { tier, same: true } : form));
      setForm({ tier, provider: "r2" });
      onDone();
    } catch (err) {
      if (err instanceof AccountChoice) setAccounts(err.accounts);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }
  const provider = form.same ? "same" : form.provider ?? "r2";
  const field = (label: string, key: keyof StorageRequest, options: { secret?: boolean; hint?: string; placeholder?: string } = {}) => (
    <div>
      <Text as="div" size="2" weight="medium" mb="1">{label}</Text>
      <TextField.Root type={options.secret ? "password" : "text"} value={String(form[key] ?? "")} onChange={(event) => set({ [key]: event.target.value })} autoComplete="off" spellCheck={false} placeholder={options.placeholder} />
      {options.hint && <Text as="div" size="1" color="gray" mt="1">{options.hint}</Text>}
    </div>
  );
  return (
    <Card size="3">
      <Flex direction="column" gap="2">
        <Flex gap="2" align="center"><Text size="3" weight="medium">Storage</Text><StatusBadge tone="gray">Not set up</StatusBadge></Flex>
        <Text size="2" color="gray">
          {tier === "scheduled" ? "Where scheduled backups are copied to." : "Where sync copies to. It can be the same place as scheduled backups, or somewhere else."}
        </Text>
        {!open ? <Button onClick={() => setOpen(true)} style={{ alignSelf: "start" }}>Choose storage</Button> : (
          <form onSubmit={submit}>
            <Flex direction="column" gap="3" style={{ maxWidth: 520 }}>
              <div>
                <Text as="div" size="2" weight="medium" mb="1">Provider</Text>
                <Select.Root value={provider} onValueChange={(value) => (value === "same" ? setForm({ tier, same: true }) : setForm({ tier, provider: value as "r2" | "s3" | "azure" }))}>
                  <Select.Trigger />
                  <Select.Content>
                    {canReuse && <Select.Item value="same">Same as scheduled backups</Select.Item>}
                    <Select.Item value="r2">Cloudflare R2 (the bucket is made for you)</Select.Item>
                    <Select.Item value="s3">S3-compatible: AWS, Google Cloud, others</Select.Item>
                    <Select.Item value="azure">Azure Blob Storage</Select.Item>
                  </Select.Content>
                </Select.Root>
              </div>
              {provider === "r2" && field("Cloudflare API token", "token", { secret: true, hint: "Needs Account API Tokens Write and Workers R2 Storage Write on the account. It is used once to make a bucket and a key that reaches only that bucket, and is not kept." })}
              {provider === "s3" && (
                <>
                  {field("Bucket", "bucket", { hint: "It must already exist." })}
                  {field("Endpoint", "endpoint", { placeholder: "https://storage.googleapis.com", hint: "Leave empty for AWS S3. For Google Cloud Storage use https://storage.googleapis.com with its interoperability (HMAC) keys." })}
                  {field("Region", "region", { placeholder: "us-east-1", hint: "Required for AWS S3. Use auto when the service has none." })}
                  {field("Access key id", "accessKeyId", { secret: true })}
                  {field("Secret access key", "secretAccessKey", { secret: true })}
                </>
              )}
              {provider === "azure" && (
                <>
                  {field("Storage account name", "account")}
                  {field("Container", "container", { hint: "It must already exist." })}
                  {field("Account key", "accountKey", { secret: true })}
                </>
              )}
              {provider === "same" && <Text size="2" color="gray">Sync will copy to the same storage as scheduled backups, in its own folder, with its own encryption password.</Text>}
              {accounts && (
                <div>
                  <Text as="div" size="2" weight="medium" mb="1">Cloudflare account</Text>
                  <Select.Root value={form.accountId ?? ""} onValueChange={(value) => set({ accountId: value })}>
                    <Select.Trigger placeholder="Choose an account" />
                    <Select.Content>{accounts.map((account) => <Select.Item key={account.id} value={account.id}>{account.name}</Select.Item>)}</Select.Content>
                  </Select.Root>
                </div>
              )}
              {error && <Problem message={error} />}
              <Flex gap="2">
                <Button type="submit" loading={busy} disabled={accounts !== null && !form.accountId}>{tier === "scheduled" ? "Save and open the pull request" : "Save"}</Button>
                <Button type="button" variant="soft" color="gray" onClick={() => { setOpen(false); setError(null); setForm({ tier, provider: "r2" }); }}>Cancel</Button>
              </Flex>
            </Flex>
          </form>
        )}
      </Flex>
    </Card>
  );
}
