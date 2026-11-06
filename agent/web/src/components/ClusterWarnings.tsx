import { Callout, Flex, Text } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { CircleCheck } from "lucide-react";
import { useState } from "react";
import { ClusterEvent, Report } from "../api";
import { DataTable } from "./DataTable";
import { StatusBadge } from "./StatusBadge";
import { formatAgo } from "../lib/format";
import { eventTarget, warningReason } from "../lib/vocabulary";

// The raw message can be very long (probe failures include whole health-check
// output). It is the technical detail, so it is clamped and expands on demand.
function Details({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <button
      type="button"
      onClick={() => setExpanded((value) => !value)}
      aria-expanded={expanded}
      title={expanded ? "Show less" : "Show more"}
      style={{ all: "unset", cursor: "pointer", display: "block", maxWidth: 520 }}
    >
      <Text size="2" color="gray" className={expanded ? "clamp expanded" : "clamp"}>{text}</Text>
    </button>
  );
}

function warningColumns(now: Date): ColumnDef<ClusterEvent, any>[] {
  return [
    {
      id: "lastSeen",
      header: "Last seen",
      accessorFn: (row) => row.lastSeen,
      cell: ({ row }) => <Text size="2" style={{ whiteSpace: "nowrap" }}>{formatAgo(row.original.lastSeen, now)}</Text>,
    },
    {
      id: "what",
      header: "Affects",
      accessorFn: (row) => `${row.object} ${row.namespace}`,
      cell: ({ row }) => {
        const target = eventTarget(row.original.object);
        return (
          <Flex direction="column">
            <Text size="2" weight="medium">{target.name}</Text>
            <Text size="1" color="gray">{[row.original.namespace, target.kind].filter(Boolean).join(" · ")}</Text>
          </Flex>
        );
      },
    },
    {
      id: "problem",
      header: "Problem",
      accessorFn: (row) => warningReason(row.reason),
      cell: ({ row }) => (
        <Flex align="center" gap="2">
          <StatusBadge tone="amber" title={row.original.reason}>{warningReason(row.original.reason)}</StatusBadge>
          {row.original.count > 1 && <Text size="1" color="gray" title="Times this has happened">×{row.original.count}</Text>}
        </Flex>
      ),
    },
    {
      id: "details",
      header: "Details",
      accessorFn: (row) => row.message,
      cell: ({ row }) => <Details text={row.original.message} />,
      enableSorting: false,
    },
  ];
}

// Every warning the cluster has reported recently, including the ones about things that are not
// part of an app (servers, certificates, volumes). An app's own are on its Overview tab.
export function ClusterWarnings({ report }: { report: Report }) {
  const warnings = report.rke2.events ?? [];
  const now = new Date(report.collectedAt);
  return warnings.length === 0 ? (
    <Callout.Root color="green">
      <Callout.Icon><CircleCheck size={16} /></Callout.Icon>
      <Callout.Text>No warnings. The cluster has nothing to report.</Callout.Text>
    </Callout.Root>
  ) : (
    <DataTable
      data={warnings}
      columns={warningColumns(now)}
      getRowId={(row) => `${row.object}/${row.reason}/${row.lastSeen}`}
      noun="warnings"
      initialSorting={[{ id: "lastSeen", desc: true }]}
    />
  );
}
