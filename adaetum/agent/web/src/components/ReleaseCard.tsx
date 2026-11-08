import { AlertDialog, Button, Callout, Card, Checkbox, Code, Flex, Select, Text } from "@radix-ui/themes";
import { useState } from "react";
import { authedFetch } from "../lib/auth";
import { ServerStatus } from "../lib/clusterApi";

interface Binary { url: string; sha256: string }
interface ReleaseCheck {
  current: string;
  updateAvailable: boolean;
  manifestHash: string;
  release: { version: string; source_commit: string; images: Record<string, string>; binaries: Record<string, Binary> };
}

// Checking upstream is explicit and read-only. The link opens the existing
// rehearsal-first Ansible form; it never starts a host update itself.
export function ReleaseCard({ servers, nodesError }: { servers: ServerStatus[]; nodesError?: string }) {
  const [channel, setChannel] = useState(() => localStorage.getItem("adaetum-release-channel") === "beta" ? "beta" : "stable");
  const [release, setRelease] = useState<ReleaseCheck | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [arch, setArch] = useState("amd64");
  const [includeAuth, setIncludeAuth] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [proposalURL, setProposalURL] = useState("");
  async function check() {
    setBusy(true); setError(""); setProposalURL("");
    try {
      const response = await authedFetch(`/api/v1/releases/latest?channel=${channel}`);
      if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status})`);
      setRelease(await response.json() as ReleaseCheck);
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  }
  async function propose() {
    if (!release) return;
    setBusy(true); setError("");
    try {
      const response = await authedFetch("/api/v1/releases/proposals", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ channel, version: release.release.version, manifestHash: release.manifestHash }) });
      if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status})`);
      const result = await response.json() as { url: string };
      setProposalURL(result.url);
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); setConfirming(false); }
  }
  const eligible = servers.filter((server) => server.status !== "stale").map((server) => server.server);
  let preview = "";
  if (release) {
    const agent = release.release.binaries[`adaetum-agent-linux-${arch}`];
    const auth = release.release.binaries[`adaetum-auth-linux-${arch}`];
    const query = new URLSearchParams({ playbook: "host-agent", limit: eligible.join(","), host_agent_binary_url: agent.url, host_agent_binary_checksum: `sha256:${agent.sha256}` });
    if (includeAuth) {
      query.set("host_agent_auth_enabled", "true");
      query.set("host_agent_auth_binary_url", auth.url);
      query.set("host_agent_auth_binary_checksum", `sha256:${auth.sha256}`);
    }
    preview = `#/automation?${query.toString()}`;
  }
  return (
    <Card size="2"><Flex direction="column" gap="3">
      <Text size="2">Check the published Adaetum agent, auth and embedding release. Checking changes nothing.</Text>
      <label><Text as="div" size="2" weight="medium">Release channel</Text><Select.Root value={channel} disabled={busy} onValueChange={(value) => { localStorage.setItem("adaetum-release-channel", value); setChannel(value); setRelease(null); setError(""); setProposalURL(""); }}><Select.Trigger /><Select.Content><Select.Item value="stable">Stable</Select.Item><Select.Item value="beta">Beta</Select.Item></Select.Content></Select.Root></label>
      <div><Button variant="soft" onClick={check} disabled={busy}>{busy ? "Checking…" : "Check for updates"}</Button></div>
      {error && <Callout.Root color="red" role="alert"><Callout.Text>{error}</Callout.Text></Callout.Root>}
      {release && <>
        <Text size="2">Running: {release.current}. Latest release: {release.release.version}{release.updateAvailable ? " — an update is available." : "."}</Text>
        <Text size="2" color="gray">The cluster images are pinned in GitOps. Create a pull request to review the three image updates; nothing is deployed until it is merged.</Text>
        {Object.entries(release.release.images).map(([name, image]) => <Text size="1" key={name}><strong>{name}</strong> <Code>{image}</Code></Text>)}
        {proposalURL ? <Text size="2"><a href={proposalURL}>Open the update pull request</a></Text> : <div><Button variant="soft" disabled={busy} onClick={() => setConfirming(true)}>Propose cluster update</Button></div>}
        <label><Text as="div" size="2" weight="medium">Host architecture</Text>
          <Select.Root value={arch} onValueChange={setArch}><Select.Trigger /><Select.Content>
            <Select.Item value="amd64">x86-64</Select.Item><Select.Item value="arm64">ARM64</Select.Item>
          </Select.Content></Select.Root>
        </label>
        <Text as="label" size="2"><Flex gap="2"><Checkbox checked={includeAuth} onCheckedChange={(checked) => setIncludeAuth(checked === true)} /> Include the host authentication service</Flex></Text>
        <Text size="1" color="gray">Select the architecture shared by the chosen servers. Quiet servers are left out. The next page starts in preview mode and lets you edit the server list.</Text>
        {nodesError || eligible.length === 0 ? <Text size="2" color="gray">A current cluster server list is needed to prepare a host update.</Text> :
          <div><Button asChild><a href={preview}>Preview host update</a></Button></div>}
      </>}
    </Flex>
      <AlertDialog.Root open={confirming} onOpenChange={setConfirming}>
        <AlertDialog.Content maxWidth="480px">
          <AlertDialog.Title>Propose this update?</AlertDialog.Title>
          <AlertDialog.Description size="2">Create a new branch and pull request for the agent, auth and embedding images and the host agent included in new installer media. Review and merge it separately to deploy. Host agents are updated through the preview below.</AlertDialog.Description>
          <Flex gap="3" mt="4" justify="end"><Button variant="soft" color="gray" onClick={() => setConfirming(false)}>Cancel</Button><Button disabled={busy} onClick={propose}>{busy ? "Creating…" : "Create pull request"}</Button></Flex>
        </AlertDialog.Content>
      </AlertDialog.Root>
    </Card>
  );
}
