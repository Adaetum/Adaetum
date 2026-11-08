import { Badge, Button, Callout, Card, Flex, Heading, Tabs, Text } from "@radix-ui/themes";
import { ArrowLeft, ExternalLink, MessageCircle } from "lucide-react";
import { Report } from "../api";
import { hrefFor } from "../app/routes";
import { AppCard, cardKey } from "../lib/apps";
import { formatBytes } from "../lib/format";
import { useSeries } from "../lib/historyApi";
import { Sizing } from "../lib/sizing";
import { Trace, traceFor } from "../lib/trace";
import { argoLink, longhornLink, rancherLink, toolHosts } from "../lib/toolLinks";
import { instanceState } from "../lib/vocabulary";
import { DeliveryPanel, PiecesPanel, ResourcesPanel, ScorePanel, StoragePanel, fixAsk } from "./AppDetail";
import { DeploymentsAdvanced, DetailsAdvanced, OverviewAdvanced, SizingAdvanced, StorageAdvanced, TraceAdvanced } from "./AppAdvanced";
import { AppLogo } from "./AppLogo";
import { StatusBadge } from "./StatusBadge";
import { useAssistant, useAssistantPage } from "./assistant/AssistantProvider";

export const APP_TABS = [
  { id: "overview", label: "Overview" },
  { id: "resources", label: "Sizing" },
  { id: "storage", label: "Storage" },
  { id: "trace", label: "How it fits" },
  { id: "deployments", label: "Deployments" },
  { id: "details", label: "Details" },
] as const;
export type AppTab = (typeof APP_TABS)[number]["id"];

export const appHref = (card: AppCard, tab?: AppTab) => hrefFor({ path: "workloads", params: tab && tab !== "overview" ? { app: cardKey(card), tab } : { app: cardKey(card) } });

// One app, on its own page. Everything here is an overview: the deeper tools (Rancher,
// Longhorn, Argo CD) are linked, not rebuilt.
export function AppPage({ card, cards, report, sizing, tab, onTab }: { card: AppCard; cards: AppCard[]; report: Report; sizing: Sizing | null; tab: AppTab; onTab: (tab: AppTab) => void }) {
  const assistant = useAssistant();
  useAssistantPage(`App page for "${card.name}" (namespace ${card.namespace}). ${card.healthy ? "It is running normally." : `Problem: ${card.problem}`}`);
  const series = useSeries(card.namespace);
  const hosts = toolHosts(report.rke2.ingresses ?? []);
  const trace = traceFor(card, cards, report.rke2.kubeServices ?? [], report.rke2.ingresses ?? []);
  const rancher = rancherLink(hosts, card);
  const argo = card.deployments[0] && argoLink(hosts, card.deployments[0]);
  const address = trace.addresses.find((item) => !item.tailnet);
  const volumeLink = (name: string) => {
    const volume = card.volumes.find((item) => item.name === name);
    return volume ? longhornLink(hosts, volume) : undefined;
  };

  return (
    <>
      <Button asChild variant="ghost" color="gray" size="1" mb="3">
        <a href={hrefFor({ path: "workloads" })}><ArrowLeft size={14} aria-hidden /> All workloads</a>
      </Button>

      <Flex gap="3" align="center" wrap="wrap" mb="4">
        <AppLogo source={{ name: card.name, chart: card.chart, repo: card.chartRepo, parts: card.parts }} label={card.name} size={56} />
        <Flex direction="column" style={{ minWidth: 0, flex: 1 }}>
          <Flex gap="2" align="center" wrap="wrap">
            <Heading size="6" as="h1">{card.name}</Heading>
            {card.system && <Badge variant="outline" color="gray">System</Badge>}
            <StatusBadge tone={card.healthy ? "green" : "red"}>{card.healthy ? "Running" : "Needs attention"}</StatusBadge>
          </Flex>
          <Text size="2" color={card.healthy ? "gray" : "red"}>{card.healthy ? "Running normally." : card.problem}</Text>
        </Flex>
        <Flex gap="2" wrap="wrap">
          {address && <Button asChild size="2" variant="soft"><a href={`https://${address.host}`} target="_blank" rel="noreferrer noopener"><ExternalLink size={14} aria-hidden /> Open {address.host}</a></Button>}
          {sizing && sizing.points < 100 && (
            <Button size="2" onClick={() => assistant.ask(fixAsk(card, sizing))}><MessageCircle size={14} aria-hidden /> Fix with assistant</Button>
          )}
        </Flex>
      </Flex>

      <Tabs.Root value={tab} onValueChange={(value) => onTab(value as AppTab)}>
        <Tabs.List>
          {APP_TABS.map((item) => <Tabs.Trigger key={item.id} value={item.id}>{item.label}</Tabs.Trigger>)}
        </Tabs.List>
        <Flex direction="column" gap="4" pt="4">
          {tab === "overview" && (
            <>
              {sizing ? <ScorePanel sizing={sizing} /> : <Text size="2" color="gray">Nothing is running and nothing is stored, so there is nothing to grade.</Text>}
              <OverviewAdvanced card={card} />
            </>
          )}
          {tab === "resources" && (
            <>
              {sizing ? <ResourcesPanel sizing={sizing} series={series} /> : <Text size="2" color="gray">Nothing is running, so there is nothing to size.</Text>}
              <SizingAdvanced card={card} report={report} />
            </>
          )}
          {tab === "storage" && (
            <>
              {sizing ? <StoragePanel card={card} sizing={sizing} series={series} linkFor={volumeLink} /> : <Text size="2" color="gray">This app stores nothing on a volume.</Text>}
              <StorageAdvanced card={card} />
            </>
          )}
          {tab === "trace" && (
            <>
              <TracePanel card={card} trace={trace} />
              <TraceAdvanced card={card} report={report} trace={trace} />
            </>
          )}
          {tab === "deployments" && (
            <>
              <Section title="How it is delivered"><DeliveryPanel card={card} linkFor={(deployment) => argoLink(hosts, deployment)} /></Section>
              {trace.deliveredWith.length > 0 && (
                <Section title="Delivered together with">
                  <Flex gap="3" wrap="wrap">
                    {trace.deliveredWith.map((other) => <a key={cardKey(other)} href={appHref(other, "deployments")}><Text size="2">{other.name}</Text></a>)}
                  </Flex>
                </Section>
              )}
              <DeploymentsAdvanced card={card} />
            </>
          )}
          {tab === "details" && (
            <>
              <Section title="Its pieces"><PiecesPanel card={card} /></Section>
              <Events card={card} report={report} />
              <DetailsAdvanced card={card} />
            </>
          )}
        </Flex>
      </Tabs.Root>

      <Flex gap="3" wrap="wrap" mt="5" align="center">
        <Text size="1" color="gray">See it in another tool:</Text>
        {rancher ? <ToolLink href={rancher}>Show in Rancher</ToolLink> : <Text size="1" color="gray">Rancher has no web address in this cluster.</Text>}
        {argo && <ToolLink href={argo}>Show in Argo CD</ToolLink>}
        {card.volumes.length > 0 && (hosts.longhorn ? <Text size="1" color="gray">Longhorn links are on the Storage tab.</Text> : <Text size="1" color="gray">Longhorn has no web address in this cluster, so volumes can't be linked.</Text>)}
      </Flex>
    </>
  );
}

function ToolLink({ href, children }: { href: string; children: string }) {
  return <Button asChild size="1" variant="ghost"><a href={href} target="_blank" rel="noreferrer noopener"><ExternalLink size={12} aria-hidden /> {children}</a></Button>;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Flex direction="column" gap="2">
      <Text size="2" weight="bold">{title}</Text>
      {children}
    </Flex>
  );
}

// Recent warnings about this app's pieces, newest first.
function Events({ card, report }: { card: AppCard; report: Report }) {
  const names = [...card.components.map((component) => component.name), ...card.instances.map((pod) => pod.name)];
  const events = (report.rke2.events ?? []).filter((event) => event.namespace === card.namespace && names.some((name) => event.object.endsWith(`/${name}`) || event.object.includes(`/${name}-`))).slice(0, 8);
  return (
    <Section title="Recent warnings">
      {events.length === 0 ? (
        <Text size="2" color="gray">None.</Text>
      ) : (
        <ul style={{ margin: 0, paddingLeft: "1.1rem" }}>
          {events.map((event) => <li key={`${event.object}/${event.reason}/${event.lastSeen}`}><Text size="1"><b>{event.reason}</b> on {event.object.split("/")[1]}: {event.message}</Text></li>)}
        </ul>
      )}
    </Section>
  );
}

// How the app is wired: the path a request takes in (address, service, copies, volumes),
// and the other apps it is connected to. The connections come from names found in the
// pods' own settings, so they are hints; a connection made through a mounted file or a
// secret can't be seen from here.
function TracePanel({ card, trace }: { card: AppCard; trace: Trace }) {
  const noCopies = trace.copies.length === 0;
  return (
    <Flex direction="column" gap="5">
      <div className="trace-flow">
        <TraceColumn title="1 · Reached at" empty="No web address points here.">
          {trace.addresses.map((address) => (
            <TraceItem key={`${address.host}${address.path}`} title={address.host + (address.path !== "/" ? address.path : "")} note={`${address.tailnet ? "tailnet only · " : address.secure ? "secure · " : ""}to ${address.service}`} />
          ))}
        </TraceColumn>
        <TraceColumn title="2 · Services" empty="No service sends traffic here.">
          {trace.services.map((service) => (
            <TraceItem key={service.name} title={service.name} note={`${service.ports.join(", ") || "no ports"} · ${service.copies === 0 ? "nothing behind it" : `${service.copies} ${service.copies === 1 ? "copy" : "copies"}`}`} tone={service.copies === 0 ? "red" : undefined} />
          ))}
        </TraceColumn>
        <TraceColumn title="3 · Running as" empty={noCopies ? "Nothing is running." : ""}>
          {trace.copies.map((pod) => {
            const state = instanceState(pod);
            return <TraceItem key={pod.name} title={pod.name} note={`${pod.node || "no server"} · ${state.label}`} tone={state.tone === "red" ? "red" : undefined} />;
          })}
        </TraceColumn>
        <TraceColumn title="4 · Stored on" empty="No volumes.">
          {trace.volumes.map((volume) => (
            <TraceItem key={volume.name} title={volume.name} note={`${volume.capacityBytes ? formatBytes(volume.capacityBytes) : volume.capacity ?? "?"}${volume.storageClass ? ` · ${volume.storageClass}` : ""}`} />
          ))}
        </TraceColumn>
      </div>

      <Section title="Other apps it is set up to use">
        {trace.uses.length === 0 && trace.usesOther.length === 0 ? <Text size="2" color="gray">None found in its settings.</Text> : (
          <Flex direction="column" gap="1">
            {trace.uses.map((link) => <AppLine key={`${link.app.namespace}/${link.app.name}/${link.via}`} card={link.app} via={link.via} />)}
            {trace.usesOther.length > 0 && <Text size="1" color="gray">Also platform services: {trace.usesOther.join(", ")}.</Text>}
          </Flex>
        )}
        <Text size="1" color="gray">Found by reading names in the settings the app was started with. Connections made through a config file or secret aren't visible.</Text>
      </Section>

      <Section title="Other apps that use it">
        {trace.usedBy.length === 0 ? <Text size="2" color="gray">No other app names it in its settings.</Text> : (
          <Flex direction="column" gap="1">{trace.usedBy.map((link) => <AppLine key={`${link.app.namespace}/${link.app.name}`} card={link.app} via={link.via} />)}</Flex>
        )}
      </Section>

      <Section title="Delivered and run with">
        <Flex direction="column" gap="1">
          <Text size="2">
            {trace.nodes.length === 0 ? "Not running on any server." : `Runs on ${trace.nodes.join(", ")}.`}
          </Text>
          {card.deployments.length > 0 && <Text size="2">Delivered by {card.deployments.map((deployment) => deployment.name).join(", ")}.</Text>}
          {trace.deliveredWith.length > 0 ? (
            <Flex gap="2" wrap="wrap" align="center">
              <Text size="2">Delivered together with</Text>
              {trace.deliveredWith.slice(0, 6).map((other) => <a key={cardKey(other)} href={appHref(other, "trace")}><Text size="2">{other.name}</Text></a>)}
              {trace.deliveredWith.length > 6 && <Text size="2" color="gray">and {trace.deliveredWith.length - 6} more</Text>}
            </Flex>
          ) : null}
        </Flex>
      </Section>
    </Flex>
  );
}

function TraceColumn({ title, empty, children }: { title: string; empty: string; children: React.ReactNode }) {
  const hasItems = Array.isArray(children) ? children.length > 0 : Boolean(children);
  return (
    <Flex direction="column" gap="2" className="trace-col">
      <Text size="1" weight="bold" color="gray">{title}</Text>
      {hasItems ? children : <Text size="1" color="gray">{empty}</Text>}
    </Flex>
  );
}

function TraceItem({ title, note, tone }: { title: string; note: string; tone?: "red" }) {
  return (
    <Card size="1" className="trace-item">
      <Flex direction="column">
        <Text size="2" weight="medium" style={{ overflowWrap: "anywhere" }}>{title}</Text>
        <Text size="1" color={tone ?? "gray"}>{note}</Text>
      </Flex>
    </Card>
  );
}

function AppLine({ card, via }: { card: AppCard; via: string }) {
  return (
    <Flex gap="2" align="center">
      <AppLogo source={{ name: card.name, chart: card.chart, repo: card.chartRepo, parts: card.parts }} label={card.name} size={20} />
      <a href={appHref(card, "trace")}><Text size="2">{card.name}</Text></a>
      <Text size="1" color="gray">through {via}</Text>
    </Flex>
  );
}

// Callout kept for a missing app so a stale link explains itself.
export function MissingApp({ id }: { id: string }) {
  return (
    <Callout.Root color="amber" role="status">
      <Callout.Text>
        No app called {id} is running. <a href={hrefFor({ path: "workloads" })}>Back to all workloads</a>
      </Callout.Text>
    </Callout.Root>
  );
}
