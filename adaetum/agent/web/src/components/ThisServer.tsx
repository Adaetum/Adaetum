import { Button, Callout, Card, Code, DataList, Flex, Heading, Text } from "@radix-ui/themes";
import { Check, Copy, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { Report } from "../api";
import { Meter } from "./Meter";
import { StatusBadge } from "./StatusBadge";
import { formatBytes, formatDuration, plural } from "../lib/format";
import { clusterService, processorType } from "../lib/vocabulary";

// The machine this agent is reporting on: its system, cluster software, services and
// resources. It sits on the Cluster page, and it is shown even when the cluster view itself can't be
// read, because on a host's own agent (the one you use when the cluster is down) this is the
// useful part of the page.
export function ThisServer({ report }: { report: Report }) {
  const { host, metrics, rke2 } = report;
  return (
    <>
      <Flex direction="column" gap="5">
        {report.errors && report.errors.length > 0 && (
          <Callout.Root color="amber" role="alert">
            <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
            <Callout.Text>
              <Text weight="medium" as="div">
                {plural(report.errors.length, "thing")} couldn't be checked, so some information may be missing.
              </Text>
              {report.errors.map((message) => (
                <Text key={message} as="div" size="1" className="mono">{message}</Text>
              ))}
            </Callout.Text>
          </Callout.Root>
        )}

        <div className="split">
          <Card size="3">
            <Heading size="4" as="h2" mb="4">System</Heading>
            <DataList.Root>
              <Fact label="Name" value={host.hostname} />
              <Fact label="Operating system" value={host.osName} />
              <Fact label="Linux kernel" value={host.kernel} mono />
              <Fact label="Processor" value={processorType(host.arch)} />
              {metrics && <Fact label="Running for" value={formatDuration(metrics.uptimeSeconds)} />}
              <Fact label="Last checked" value={new Date(report.collectedAt).toLocaleString()} />
            </DataList.Root>
          </Card>

          <Card size="3">
            <Heading size="4" as="h2" mb="4">Cluster software</Heading>
            <DataList.Root>
              <Fact label="Version" value={rke2.version ?? "Unknown"} mono />
              {/* Controller first: it is the service that matters on a server like this. */}
              {Object.entries(rke2.services)
                .sort(([a], [b]) => Number(b === "rke2-server") - Number(a === "rke2-server"))
                .map(([unit, state]) => {
                const service = clusterService(unit, state);
                return (
                  <DataList.Item key={unit} align="center">
                    <DataList.Label minWidth="130px">{service.name}</DataList.Label>
                    <DataList.Value>
                      <StatusBadge tone={service.tone} title={`${unit}: ${state}`}>{service.state}</StatusBadge>
                    </DataList.Value>
                  </DataList.Item>
                );
              })}
            </DataList.Root>
          </Card>
        </div>

        <Card size="3">
          <Heading size="4" as="h2" mb="1">System services</Heading>
          {host.failedUnits?.length ? (
            <>
              <Text size="2" color="gray" as="p" mb="3">
                {plural(host.failedUnits.length, "background service")} stopped unexpectedly. The names are technical; they are
                shown exactly as the system reports them.
              </Text>
              <Flex direction="column" gap="2">
                {host.failedUnits.map((unit) => (
                  <Code key={unit} size="2" color="red" style={{ overflowWrap: "anywhere" }}>{unit}</Code>
                ))}
              </Flex>
            </>
          ) : (
            <Text size="2" color="gray">All system services are running normally.</Text>
          )}
        </Card>

        {metrics && (
          <Card size="3">
            <Heading size="4" as="h2" mb="4">Resources</Heading>
            <Flex direction="column" gap="4">
              <Meter
                label="Processor load"
                fraction={metrics.cpuCount > 0 ? metrics.load1 / metrics.cpuCount : 0}
                detail={`${metrics.load1.toFixed(2)} on ${plural(metrics.cpuCount, "core")} (last minute)`}
              />
              <Meter
                label="Memory"
                fraction={metrics.memTotalBytes > 0 ? (metrics.memTotalBytes - metrics.memAvailableBytes) / metrics.memTotalBytes : 0}
                detail={`${formatBytes(metrics.memTotalBytes - metrics.memAvailableBytes)} of ${formatBytes(metrics.memTotalBytes)}`}
              />
              {metrics.swapTotalBytes > 0 && (
                <Meter
                  label="Swap"
                  fraction={(metrics.swapTotalBytes - metrics.swapFreeBytes) / metrics.swapTotalBytes}
                  detail={`${formatBytes(metrics.swapTotalBytes - metrics.swapFreeBytes)} of ${formatBytes(metrics.swapTotalBytes)}`}
                />
              )}
              {metrics.disks?.map((disk) => (
                <Meter
                  key={disk.mount}
                  label={`Disk ${disk.mount}`}
                  fraction={disk.totalBytes > 0 ? disk.usedBytes / disk.totalBytes : 0}
                  detail={`${formatBytes(disk.usedBytes)} of ${formatBytes(disk.totalBytes)}`}
                />
              ))}
            </Flex>
          </Card>
        )}

        <TechnicalDetails report={report} />
      </Flex>
    </>
  );
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <DataList.Item>
      <DataList.Label minWidth="130px">{label}</DataList.Label>
      <DataList.Value>{mono ? <Code size="2" variant="ghost">{value}</Code> : value || "—"}</DataList.Value>
    </DataList.Item>
  );
}

// Everything above is a simplified view. The raw report is the exact data the
// agent collected, for support and debugging.
function TechnicalDetails({ report }: { report: Report }) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const json = JSON.stringify(report, null, 2);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(json);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard access can be denied; the text stays selectable */
    }
  };

  return (
    <Card size="3">
      <Flex justify="between" align="center" gap="3" wrap="wrap" mb={open ? "4" : "0"}>
        <Flex direction="column">
          <Heading size="4" as="h2">Technical details</Heading>
          <Text size="2" color="gray">The exact data the agent collected, for support and troubleshooting.</Text>
        </Flex>
        <Flex gap="2">
          {open && (
            <Button variant="soft" color="gray" onClick={copy}>
              {copied ? <Check size={14} /> : <Copy size={14} />}
              {copied ? "Copied" : "Copy"}
            </Button>
          )}
          <Button variant="soft" color="gray" onClick={() => setOpen((value) => !value)} aria-expanded={open}>
            {open ? "Hide" : "Show"}
          </Button>
        </Flex>
      </Flex>
      {open && <pre className="raw-json">{json}</pre>}
    </Card>
  );
}
