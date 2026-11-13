import { Badge, Box, Button, Card, Flex, Heading, Separator, Text } from "@radix-ui/themes";
import { ChevronRight, CircleAlert, CircleCheck, GitBranch, HardDrive, Layers, MessageCircle, Server, ShieldCheck, TriangleAlert } from "lucide-react";
import { Report } from "../api";
import { hrefFor } from "../app/routes";
import { Meter } from "../components/Meter";
import { PageHeader } from "../components/PageHeader";
import { StatTile } from "../components/StatTile";
import { StatusBadge } from "../components/StatusBadge";
import { summarizeApps } from "../lib/apps";
import { AttentionItem, summarizeAttention } from "../lib/attention";
import { formatAgo, formatBytes, formatDuration, plural } from "../lib/format";
import { summarizeStorage } from "../lib/storage";
import { Tone, argoConverged, certState, daysUntil, nodeProblem, toneForUsage, volumeProblem } from "../lib/status";
import { pressureLabel, serverRole } from "../lib/vocabulary";
import { useAssistant, useAssistantPage } from "../components/assistant/AssistantProvider";

export function Overview({ report }: { report: Report }) {
  const now = new Date(report.collectedAt);
  const attention = summarizeAttention(report);
  const critical = attention.filter((item) => item.severity === "critical").length;
  // The assistant is told exactly what the health card says, not left to read it off the screen.
  useAssistantPage(
    attention.length === 0
      ? "Overview: every check on this server and cluster is passing."
      : `Overview. Problems and warnings shown, most important first:\n${attention
          .map((item) => `- ${item.severity}: ${item.title}${item.detail ? ` (${item.detail})` : ""}`)
          .join("\n")}`,
  );

  return (
    <>
      <PageHeader
        title="Overview"
        description={`${report.host.hostname || "Whole cluster"} · checked ${formatAgo(report.collectedAt, new Date())}`}
      />
      <Flex direction="column" gap="5">
        <HealthCard attention={attention} critical={critical} />
        <Tiles report={report} now={now} />
        <div className="split">
          <ResourcesCard report={report} />
          <ServersCard report={report} />
        </div>
      </Flex>
    </>
  );
}

// askAboutProblem words a dashboard item as the plain request a person would type,
// so the assistant starts from exactly what the page showed.
function askAboutProblem(item: AttentionItem): string {
  const detail = item.detail ? ` (${item.detail})` : "";
  return `The dashboard says: ${item.title}${detail}. What does that mean, and can you help me fix it?`;
}

function HealthCard({ attention, critical }: { attention: AttentionItem[]; critical: number }) {
  const assistant = useAssistant();
  const tone: Tone = critical > 0 ? "red" : attention.length > 0 ? "amber" : "green";
  const title =
    attention.length === 0
      ? "Everything is running normally"
      : critical > 0
        ? `${plural(critical, "problem")}${attention.length > critical ? `, ${plural(attention.length - critical, "warning")}` : ""}`
        : `${plural(attention.length, "thing")} to keep an eye on`;
  const Icon = tone === "green" ? CircleCheck : tone === "red" ? CircleAlert : TriangleAlert;

  return (
    <Card size="3" style={{ padding: 0, overflow: "hidden" }}>
      <Flex align="center" gap="4" p="4" style={{ background: `var(--${tone}-a3)` }}>
        <Box style={{ color: `var(--${tone}-11)`, display: "flex" }}>
          <Icon size={28} aria-hidden />
        </Box>
        <Flex direction="column">
          <Heading size="5" as="h2">{title}</Heading>
          <Text size="2" color="gray">
            {attention.length === 0 ? "Every check on this server and cluster is passing." : "Most important first. Select an item to see the details."}
          </Text>
        </Flex>
      </Flex>
      {attention.map((item) => (
        <div key={item.id} className="attention-item">
          <a className="attention-row" href={hrefFor(item.to)}>
            <Box style={{ color: `var(--${item.severity === "critical" ? "red" : "amber"}-11)`, paddingTop: 2, display: "flex" }}>
              {item.severity === "critical" ? <CircleAlert size={18} aria-label="Problem" /> : <TriangleAlert size={18} aria-label="Warning" />}
            </Box>
            <Flex direction="column" style={{ flex: 1, minWidth: 0 }}>
              <Text size="2" weight="medium">{item.title}</Text>
              {item.detail && <Text size="2" color="gray" className="mono">{item.detail}</Text>}
            </Flex>
            <ChevronRight size={16} color="var(--gray-9)" aria-hidden style={{ marginTop: 3 }} />
          </a>
          {/* The hand-off from "I see a problem" to "help me fix it": pressing opens the
              assistant with the problem already asked. Reading is automatic; any change
              it proposes still waits for the person's approval in the drawer. */}
          <Button variant="soft" size="1" className="attention-fix" onClick={() => assistant.ask(askAboutProblem(item))} aria-label={`Fix with the assistant: ${item.title}`}>
            <MessageCircle size={14} aria-hidden /> Fix with assistant
          </Button>
        </div>
      ))}
    </Card>
  );
}

function Tiles({ report, now }: { report: Report; now: Date }) {
  const { rke2 } = report;
  const servers = rke2.nodes ?? [];
  const apps = summarizeApps(rke2.workloads ?? [], rke2.pods ?? []);
  const deployments = rke2.argoApps ?? [];
  const certificates = rke2.certificates ?? [];
  const volumes = rke2.volumes ?? [];

  const serversReady = servers.filter((server) => !nodeProblem(server)).length;
  const appsRunning = apps.filter((app) => app.healthy).length;
  const deploymentsOk = deployments.filter(argoConverged).length;
  const volumeProblems = volumes.filter(volumeProblem).length;
  const capacity = rke2.storageCapacity && rke2.storageCapacity.disks.length > 0 ? summarizeStorage(rke2.storageCapacity, volumes) : null;
  const certProblems = certificates.filter((certificate) => certState(certificate, now) !== "ok").length;
  const nextExpiry = certificates
    .map((certificate) => certificate.notAfter)
    .filter((value): value is string => Boolean(value))
    .map((value) => daysUntil(value, now))
    .sort((a, b) => a - b)[0];

  return (
    <div className="tile-grid">
      <StatTile
        icon={<Server size={16} />}
        label="Servers"
        value={`${serversReady} of ${servers.length}`}
        caption={serversReady === servers.length ? "All ready" : `${servers.length - serversReady} need attention`}
        tone={serversReady === servers.length ? "green" : "red"}
        to={{ path: "overview" }}
      />
      <StatTile
        icon={<Layers size={16} />}
        label="Apps"
        value={`${appsRunning} of ${apps.length}`}
        caption={appsRunning === apps.length ? "All running" : `${apps.length - appsRunning} need attention`}
        tone={appsRunning === apps.length ? "green" : "red"}
        to={{ path: "workloads", params: { problems: "1" } }}
      />
      <StatTile
        icon={<GitBranch size={16} />}
        label="Deployments"
        value={`${deploymentsOk} of ${deployments.length}`}
        caption={deploymentsOk === deployments.length ? "All up to date" : `${deployments.length - deploymentsOk} need attention`}
        tone={deploymentsOk === deployments.length ? "green" : "amber"}
        to={{ path: "workloads", params: { focus: "deployments" } }}
      />
      {capacity ? (
        <StatTile
          icon={<HardDrive size={16} />}
          label="Storage"
          value={`${Math.round(capacity.usedFraction * 100)}% used`}
          caption={volumeProblems > 0 ? `${plural(volumeProblems, "volume")} with problems` : `${formatBytes(capacity.usedBytes)} of ${formatBytes(capacity.totalBytes)}`}
          tone={volumeProblems > 0 ? "red" : toneForUsage(capacity.fullestFraction)}
          to={{ path: "workloads", params: { focus: "storage" } }}
        />
      ) : (
        <StatTile
          icon={<HardDrive size={16} />}
          label="Storage"
          value={plural(volumes.length, "volume")}
          caption={volumeProblems === 0 ? "All healthy" : `${volumeProblems} with problems`}
          tone={volumeProblems === 0 ? "green" : "red"}
          to={{ path: "workloads", params: { focus: "storage" } }}
        />
      )}
      <StatTile
        icon={<ShieldCheck size={16} />}
        label="Certificates"
        value={String(certificates.length)}
        caption={
          certProblems > 0
            ? `${certProblems} need attention`
            : nextExpiry === undefined
              ? "None issued"
              : `Next renewal in ${plural(nextExpiry, "day")}`
        }
        tone={certProblems === 0 ? "green" : "amber"}
        to={{ path: "workloads", params: { focus: "certificates" } }}
      />
    </div>
  );
}

function ResourcesCard({ report }: { report: Report }) {
  const metrics = report.metrics;
  return (
    <Card size="3">
      <Flex justify="between" align="baseline" mb="4">
        <Heading size="4" as="h2">Resources</Heading>
        {metrics && <Text size="2" color="gray">Running for {formatDuration(metrics.uptimeSeconds)}</Text>}
      </Flex>
      {!metrics ? (
        <Text size="2" color="gray">
          {report.host.hostname
            ? "Resource figures aren't available for this server."
            : "Processor, memory and disk figures belong to one machine, so they are on each server's own agent."}
        </Text>
      ) : (
        <Flex direction="column" gap="4">
          {metrics.cpuCount > 0 && (
            <Meter
              label="Processor load"
              fraction={metrics.load1 / metrics.cpuCount}
              detail={`${metrics.load1.toFixed(2)} on ${plural(metrics.cpuCount, "core")}`}
            />
          )}
          {metrics.memTotalBytes > 0 && (
            <Meter
              label="Memory"
              fraction={(metrics.memTotalBytes - metrics.memAvailableBytes) / metrics.memTotalBytes}
              detail={`${formatBytes(metrics.memTotalBytes - metrics.memAvailableBytes)} of ${formatBytes(metrics.memTotalBytes)}`}
            />
          )}
          {[...(metrics.disks ?? [])]
            .sort((a, b) => b.usedBytes / b.totalBytes - a.usedBytes / a.totalBytes)
            .slice(0, 3)
            .map((disk) => (
              <Meter
                key={disk.mount}
                label={`Disk ${disk.mount}`}
                fraction={disk.totalBytes > 0 ? disk.usedBytes / disk.totalBytes : 0}
                detail={`${formatBytes(disk.usedBytes)} of ${formatBytes(disk.totalBytes)}`}
              />
            ))}
        </Flex>
      )}
    </Card>
  );
}

function ServersCard({ report }: { report: Report }) {
  const servers = report.rke2.nodes ?? [];
  return (
    <Card size="3">
      <Flex justify="between" align="baseline" mb="4">
        <Heading size="4" as="h2">Servers</Heading>
        {report.rke2.version && <Text size="2" color="gray">Cluster software {report.rke2.version}</Text>}
      </Flex>
      {servers.length === 0 ? (
        <Text size="2" color="gray">No servers reported yet.</Text>
      ) : (
        servers.map((server, index) => (
          <Box key={server.name}>
            {index > 0 && <Separator size="4" my="3" />}
            <Flex justify="between" align="center" gap="3">
              <Flex direction="column" style={{ minWidth: 0 }}>
                <Text size="2" weight="medium" truncate>{server.name}</Text>
                <Text size="1" color="gray" className="mono">{server.internalIp ?? ""}</Text>
              </Flex>
              <Flex gap="2" align="center" wrap="wrap" justify="end">
                {[...new Set(server.roles?.map(serverRole))].map((role) => (
                  <Badge key={role} color="gray" variant="outline" size="1">{role}</Badge>
                ))}
                <StatusBadge tone={nodeProblem(server) ? "red" : server.cordoned ? "amber" : "green"}>
                  {!server.ready ? "Not ready" : server.pressure?.length ? server.pressure.map(pressureLabel).join(", ") : server.cordoned ? "Not accepting new work" : "Ready"}
                </StatusBadge>
              </Flex>
            </Flex>
          </Box>
        ))
      )}
    </Card>
  );
}
