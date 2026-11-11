import { Badge, Button, Card, Code, Dialog, Flex, Separator, Text } from "@radix-ui/themes";
import { MessageCircle } from "lucide-react";
import { useState } from "react";
import { Node } from "../api";
import { ServerState, ServerStatus } from "../lib/clusterApi";
import { formatAgo, formatBytes, plural } from "../lib/format";
import { HostScore, hostFixAsk, scoreHost } from "../lib/hostScore";
import { Tone } from "../lib/status";
import { useAssistant } from "./assistant/AssistantProvider";
import { Meter } from "./Meter";
import { Radar, SectionChips } from "./Scorecard";
import { StatusBadge } from "./StatusBadge";

// What each agent state means to a person, not to the code. "Agent" is the Adaetum program
// that runs on a server; "server" is a machine in the cluster.
export const STATE: Record<ServerState, { label: string; tone: Tone; meaning: string }> = {
  ok: { label: "Running", tone: "green", meaning: "The agent is running and up to date." },
  missing: { label: "Agent not installed", tone: "red", meaning: "This server is in the cluster, but no agent has ever reported from it." },
  stale: { label: "Not heard from lately", tone: "amber", meaning: "The agent reported before but has gone quiet. The server may be off, or the agent stopped." },
  outdated: { label: "Needs an update", tone: "amber", meaning: "The agent is running an older version than the others." },
};

interface HostCard {
  server: ServerStatus;
  node?: Node;
  score: HostScore | null;
}

// Joins each server in the cluster with what Kubernetes already says about it, so a server
// Kubernetes has not reported on still gets a card that says so. Nothing is measured on the
// machine for this: the cluster's own view is the source.
export function hostCards(servers: ServerStatus[], nodes: Node[]): HostCard[] {
  const byName = new Map(nodes.map((node) => [node.name, node]));
  return servers.map((server) => {
    const node = byName.get(server.server);
    return { server, node, score: scoreHost(node, nodes, server.status === "outdated") };
  });
}

// The few facts a person most wants about a machine without opening it.
function Facts({ node, now }: { node: Node; now: Date }) {
  return (
    <Flex gap="2" wrap="wrap">
      {(node.roles ?? []).includes("control-plane") && <Badge variant="outline" color="gray">Control plane</Badge>}
      {node.cordoned && <Badge variant="outline" color="amber">Paused</Badge>}
      {node.version && <Badge variant="outline" color="gray">Kubernetes {node.version}</Badge>}
      {node.readySince && node.ready && <Badge variant="outline" color="gray">Ready for {formatSince(node.readySince, now)}</Badge>}
    </Flex>
  );
}

// "3d 4h" rather than "3d 4h ago": it says how long the machine has stayed ready.
const formatSince = (iso: string, now: Date) => formatAgo(iso, now).replace(/ ago$/, "");

function Meters({ node }: { node: Node }) {
  if (!node.hasUsage) return null;
  const cpu = node.allocatableCpuMillis && node.usageCpuMillis !== undefined ? node.usageCpuMillis / node.allocatableCpuMillis : undefined;
  const memory = node.allocatableMemoryBytes && node.usageMemoryBytes !== undefined ? node.usageMemoryBytes / node.allocatableMemoryBytes : undefined;
  return (
    <Flex direction="column" gap="2">
      {cpu !== undefined && <Meter label="Processing" fraction={cpu} detail={`${Math.round(cpu * 100)}% in use`} />}
      {memory !== undefined && <Meter label="Memory" fraction={memory} detail={`${Math.round(memory * 100)}% in use (${formatBytes(node.usageMemoryBytes ?? 0)} of ${formatBytes(node.allocatableMemoryBytes ?? 0)})`} />}
    </Flex>
  );
}

// Up to about six cards at a time; the grid wraps, so more servers just add rows.
export function HostCards({ cards, now }: { cards: HostCard[]; now: Date }) {
  const [open, setOpen] = useState<HostCard | null>(null);
  if (cards.length === 0) return <Text color="gray" as="p">The cluster lists no servers.</Text>;
  return (
    <>
      <ul className="host-grid">
        {cards.map((card) => {
          const state = STATE[card.server.status];
          const label = `${card.server.server}: ${state.label}. Show details.`;
          return (
            <li key={card.server.server}>
              <Card asChild size="2" className="app-card">
                <button type="button" onClick={() => setOpen(card)} aria-label={label}>
                  <Flex direction="column" gap="3">
                    <Flex justify="between" align="center" gap="2">
                      <Code variant="ghost" size="3" truncate>{card.server.server}</Code>
                      <StatusBadge tone={state.tone} title={state.meaning}>{state.label}</StatusBadge>
                    </Flex>
                    {card.score && card.node ? (
                      <>
                        <Flex gap="3" align="center">
                          <Radar sizing={card.score} />
                          <Flex direction="column" gap="1" style={{ minWidth: 0, flex: 1 }}>
                            <Text size="2" weight="bold" color={card.score.tone === "gray" ? undefined : card.score.tone}>Health {card.score.points}/100 · {card.score.grade}</Text>
                            <SectionChips sizing={card.score} />
                            {card.score.top[0] && <Text size="1" color={card.score.grade === "Good" ? "gray" : card.score.tone}>{card.score.top[0]}</Text>}
                          </Flex>
                        </Flex>
                        <Meters node={card.node} />
                        <Facts node={card.node} now={now} />
                      </>
                    ) : (
                      <Text size="2" color="gray">
                        Kubernetes has not reported on this server yet, so there is nothing to score.
                      </Text>
                    )}
                    {card.server.lastSeen && <Text size="1" color="gray">Last heard from {formatAgo(card.server.lastSeen, now)}</Text>}
                  </Flex>
                </button>
              </Card>
            </li>
          );
        })}
      </ul>
      <HostDetail card={open} now={now} onClose={() => setOpen(null)} />
    </>
  );
}

// The detail view: the score, each finding in plain words with a button that hands it to the
// assistant, and the facts behind the card. The assistant explains and guides; it never
// changes the machine, exactly as for workloads.
function HostDetail({ card, now, onClose }: { card: HostCard | null; now: Date; onClose: () => void }) {
  const assistant = useAssistant();
  return (
    <Dialog.Root open={card !== null} onOpenChange={(next) => { if (!next) onClose(); }}>
      <Dialog.Content maxWidth="640px">
        {card && (
          <>
            <Dialog.Title>{card.server.server}</Dialog.Title>
            <Dialog.Description size="2" color="gray">{STATE[card.server.status].meaning}</Dialog.Description>
            {card.score && card.node ? (
              <Flex direction="column" gap="4" mt="4">
                <Flex gap="4" align="center">
                  <Radar sizing={card.score} size={110} />
                  <Flex direction="column" gap="1">
                    <Text size="4" weight="bold" color={card.score.tone === "gray" ? undefined : card.score.tone}>{card.score.points}/100 · {card.score.grade}</Text>
                    <Text size="2" color="gray">
                      {card.score.issues.length === 0 ? "Nothing needs fixing on this server." : `${plural(card.score.issues.length, "thing")} to fix.`}
                    </Text>
                    {card.score.issues.length > 0 && (
                      <Button size="2" mt="1" onClick={() => { assistant.ask(hostFixAsk(card.server.server, card.score as HostScore)); onClose(); }}>
                        <MessageCircle size={14} aria-hidden /> Fix with assistant
                      </Button>
                    )}
                  </Flex>
                </Flex>
                <Separator size="4" />
                {card.score.sections.map((section) => (
                  <Flex key={section.key} direction="column" gap="1">
                    <Flex justify="between" align="baseline">
                      <Text size="2" weight="bold">{section.label}</Text>
                      <Text size="2" color={section.points === null ? "gray" : undefined}>{section.points === null ? "Not reported yet" : `${section.points}/100`}</Text>
                    </Flex>
                    <Text size="1" color="gray">{section.asks}</Text>
                    {section.reasons.length === 0 && section.points !== null && <Text size="2" color="green">All good.</Text>}
                    {card.score?.issues.filter((item) => item.section === section.key).map((item) => (
                      <Flex key={item.text} justify="between" align="center" gap="3">
                        <Text size="2">{item.text}</Text>
                        <Button size="1" variant="soft" onClick={() => { assistant.ask(hostFixAsk(card.server.server, card.score as HostScore, item)); onClose(); }}>Fix</Button>
                      </Flex>
                    ))}
                  </Flex>
                ))}
                <Separator size="4" />
                <Meters node={card.node} />
                <Facts node={card.node} now={now} />
                {(card.node.osImage || card.node.kernel) && <Text size="1" color="gray">{[card.node.osImage, card.node.kernel && `kernel ${card.node.kernel}`].filter(Boolean).join(" · ")}</Text>}
              </Flex>
            ) : (
              <Text as="p" size="2" mt="4">Kubernetes has not reported on this server yet. Once it does, its score and anything to fix will show here.</Text>
            )}
            <Flex justify="end" mt="4"><Dialog.Close><Button variant="soft" color="gray">Close</Button></Dialog.Close></Flex>
          </>
        )}
      </Dialog.Content>
    </Dialog.Root>
  );
}
