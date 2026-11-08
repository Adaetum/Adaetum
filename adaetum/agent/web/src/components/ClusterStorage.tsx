import { Callout, Card, Flex, Heading, Progress, Table, Text } from "@radix-ui/themes";
import { Info } from "lucide-react";
import { Report } from "../api";
import { CleanupCard } from "./cleanup/CleanupCard";
import { DEFAULT_DISK_PER_COMPRESSED } from "../lib/cleanupText";
import { ScrollRegion } from "./ScrollRegion";
import { StackedBar } from "./StackedBar";
import { formatBytes } from "../lib/format";
import { DiskSummary, StorageSummary, diskPerCompressedByte, summarizeStorage } from "../lib/storage";
import { toneForUsage } from "../lib/status";

const percent = (fraction: number) => `${Math.round(fraction * 100)}%`;

// The cluster-wide half of storage: disk capacity, how it is set aside, and image cleanup.
// Shared by this page and the Workloads page's Advanced section, so both say the same.
export function ClusterStorage({ report, refresh }: { report: Report; refresh: () => void }) {
  const volumes = report.rke2.volumes ?? [];
  const capacity = report.rke2.storageCapacity;
  const summary =
    capacity && capacity.disks.length > 0
      ? summarizeStorage(capacity, volumes, { images: report.host.containerImages, hostname: report.host.hostname })
      : null;
  return (
    <Flex direction="column" gap="5">
      {summary ? (
        <Capacity summary={summary} />
      ) : (
        <Callout.Root color="gray">
          <Callout.Icon><Info size={16} /></Callout.Icon>
          <Callout.Text>
            Disk capacity isn't available for this cluster, so only the list of app storage is shown.
          </Callout.Text>
        </Callout.Root>
      )}
      <CleanupCard
        diskRatio={(summary && diskPerCompressedByte(summary, report.host.containerImages)) || DEFAULT_DISK_PER_COMPRESSED}
        onChanged={refresh}
      />
    </Flex>
  );
}

// The unused-image figure is only known for the server the agent reports on, so
// the sentence names that server instead of implying a cluster-wide number.
function imagesDescription(summary: StorageSummary): string {
  const base = "Downloaded program files that apps run from. Ones no longer in use are usually cleared automatically when a disk is over 85% full.";
  const checked = summary.disks.find((disk) => disk.unusedImageBytes !== undefined);
  const unused = checked && checked.unusedImageBytes !== undefined ? ` On ${checked.node}, about ${formatBytes(checked.unusedImageBytes)} are not used by any app.` : "";
  // Be explicit about the gap rather than letting the numbers look complete.
  const partial = summary.imageCopiesPartial
    ? " Compressed copies of images could only be measured on one server, so on the others they are counted under system and other files."
    : "";
  return `${base}${unused}${partial}`;
}

function Capacity({ summary }: { summary: StorageSummary }) {
  const { fullest } = summary;
  const tone = toneForUsage(summary.fullestFraction);

  return (
    <>
      <Card size="3" style={{ padding: 0, overflow: "hidden" }}>
        <Flex direction="column" gap="1" p="4" style={{ background: `var(--${tone}-a3)` }}>
          <Heading size="5" as="h2">{percent(summary.usedFraction)} of storage is in use</Heading>
          <Text size="2" color="gray">
            {formatBytes(summary.usedBytes)} of {formatBytes(summary.totalBytes)}.
            {summary.disks.length > 1 && fullest ? ` The fullest disk is on ${fullest.node} at ${percent(fullest.usedFraction)}.` : ""}
          </Text>
        </Flex>
      </Card>

      <div className="split">
        <Card size="3">
          <Heading size="4" as="h2" mb="1">How space is set aside</Heading>
          <Text size="2" color="gray" as="p" mb="4">
            Apps ask for a size when they are created. This is what has been promised, not what has been written.
          </Text>
          <StackedBar
            title={`${formatBytes(summary.totalBytes)} total`}
            totalBytes={summary.totalBytes}
            segments={[
              {
                label: "Reserved",
                bytes: summary.reservedBytes,
                color: "var(--gray-9)",
                hatched: true,
                description: "Held back for the system. Apps are never placed here.",
              },
              {
                label: "Allocated",
                bytes: Math.min(summary.allocatedBytes, Math.max(0, summary.totalBytes - summary.reservedBytes)),
                color: "var(--accent-9)",
                description: "Promised to apps. Volumes only use what they write, so this can be more than is used.",
              },
              {
                label: "Unallocated",
                bytes: summary.unallocatedBytes,
                color: "var(--gray-a5)",
                description: "Not yet promised to any app.",
              },
            ]}
          />
          {summary.overAllocated && (
            <Text size="2" color="amber" as="p" mt="3">
              More space has been promised ({formatBytes(summary.allocatedBytes)}) than the disks can hold once the reserved space is
              removed. That is allowed because volumes grow on demand, but keep an eye on how full the disks get.
            </Text>
          )}
        </Card>

        <Card size="3">
          <Heading size="4" as="h2" mb="1">How much is actually used</Heading>
          <Text size="2" color="gray" as="p" mb="4">What is stored on the disks right now.</Text>
          <StackedBar
            title={`${formatBytes(summary.usedBytes)} used`}
            totalBytes={summary.totalBytes}
            segments={[
              {
                label: "Apps",
                bytes: summary.appsBytes,
                color: "var(--accent-9)",
                description: "Data written by apps, including every copy kept for safety.",
              },
              ...(summary.imagesBytes > 0
                ? [
                    {
                      label: "Container images",
                      bytes: summary.imagesBytes,
                      color: "var(--teal-9)",
                      description: imagesDescription(summary),
                    },
                  ]
                : []),
              {
                label: "System and other files",
                bytes: summary.systemBytes,
                color: "var(--gray-9)",
                description: "Operating system, cluster data, logs, and anything else on these disks.",
              },
              { label: "Free", bytes: summary.freeBytes, color: "var(--gray-a5)", description: "Space left on the disks." },
            ]}
          />
        </Card>
      </div>

      {summary.disks.length > 1 && <Disks disks={summary.disks} />}
    </>
  );
}

function Disks({ disks }: { disks: DiskSummary[] }) {
  return (
    <Flex direction="column" gap="3">
      <Heading size="4" as="h2">By server</Heading>
      <ScrollRegion label="Storage by server" wrap>
        <Table.Root variant="surface" size="2">
          <Table.Header>
            <Table.Row>
              <Table.ColumnHeaderCell>Server</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>Total</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>Reserved</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>Allocated</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>Container images</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>Not in use</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>In use</Table.ColumnHeaderCell>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {disks.map((disk) => (
              <Table.Row key={`${disk.node}:${disk.path}`} align="center">
                <Table.RowHeaderCell>
                  <Text size="2" weight="medium" as="div">{disk.node}</Text>
                  {!disk.schedulable && <Text size="1" color="amber">Not accepting new volumes</Text>}
                </Table.RowHeaderCell>
                <Table.Cell>{formatBytes(disk.totalBytes)}</Table.Cell>
                <Table.Cell>{disk.reservedBytes > 0 ? formatBytes(disk.reservedBytes) : "None"}</Table.Cell>
                <Table.Cell>{formatBytes(disk.allocatedBytes)}</Table.Cell>
                <Table.Cell>
                  {disk.imagesTotalBytes > 0 ? (
                    <>
                      <Text size="2" as="div">{formatBytes(disk.imagesTotalBytes)}</Text>
                      {disk.imageCopiesBytes > 0 && (
                        <Text size="1" color="gray" as="div">includes {formatBytes(disk.imageCopiesBytes)} of compressed copies</Text>
                      )}
                    </>
                  ) : (
                    "—"
                  )}
                </Table.Cell>
                <Table.Cell>
                  {disk.unusedImageBytes !== undefined ? (
                    <>
                      <Text size="2" as="div">about {formatBytes(disk.unusedImageBytes)}</Text>
                      <Text size="1" color="gray" as="div">{percent(disk.unusedImageShare ?? 0)} of its images</Text>
                    </>
                  ) : (
                    <Text size="2" color="gray">Not checked</Text>
                  )}
                </Table.Cell>
                <Table.Cell style={{ minWidth: 140 }}>
                  <Flex align="center" gap="3">
                    <Progress
                      value={Math.round(disk.usedFraction * 100)}
                      color={toneForUsage(disk.usedFraction)}
                      size="2"
                      aria-label={`${disk.node} storage in use`}
                    />
                    <Text size="2" style={{ minWidth: "4ch", textAlign: "right" }}>{percent(disk.usedFraction)}</Text>
                  </Flex>
                </Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table.Root>
      </ScrollRegion>
      {disks.some((disk) => disk.unusedImageBytes !== undefined) && disks.some((disk) => disk.unusedImageBytes === undefined) && (
        <Text size="1" color="gray">
          Unused images are only checked on the server the agent runs on, because only that server's container runtime can say. The
          figure is an estimate: it applies the share of images no app uses to the space images actually take.
        </Text>
      )}
    </Flex>
  );
}
