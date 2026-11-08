import { Code, Flex, Text } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { Lock, LockOpen } from "lucide-react";
import { Certificate, Ingress, Report } from "../api";
import { DataTable } from "./DataTable";
import { StatusBadge } from "./StatusBadge";
import { formatDaysRemaining } from "../lib/format";
import { certState, daysUntil } from "../lib/status";

const addressColumns: ColumnDef<Ingress, any>[] = [
  {
    id: "address",
    header: "Address",
    accessorFn: (row) => `${row.hosts.join(" ")} ${row.name}`,
    cell: ({ row }) => (
      <Flex direction="column" align="start" gap="1">
        {row.original.hosts.length === 0 ? (
          <Text size="2" color="gray">{row.original.name}</Text>
        ) : (
          row.original.hosts.map((host) => <Code key={host} size="2" variant="ghost">{host}</Code>)
        )}
      </Flex>
    ),
  },
  { id: "app", header: "App", accessorFn: (row) => row.namespace },
  {
    id: "https",
    header: "Secure connection",
    accessorFn: (row) => String(row.tls),
    // "Not set here" rather than a warning: encryption may be handled by a
    // proxy in front of the cluster, which this view cannot see.
    cell: ({ row }) =>
      row.original.tls ? (
        <Flex align="center" gap="1"><Lock size={14} aria-hidden /><Text size="2">HTTPS</Text></Flex>
      ) : (
        <Flex align="center" gap="1" style={{ color: "var(--gray-11)" }}><LockOpen size={14} aria-hidden /><Text size="2">Not set here</Text></Flex>
      ),
  },
];

function certificateColumns(now: Date): ColumnDef<Certificate, any>[] {
  return [
    {
      id: "name",
      header: "Certificate",
      accessorFn: (row) => `${row.name} ${row.namespace}`,
      cell: ({ row }) => (
        <Flex direction="column" gap="1">
          <Text size="2" weight="medium">{row.original.name}</Text>
          <Text size="1" color="gray">{row.original.namespace}</Text>
          {row.original.message && <Text size="1" color="red">{row.original.message}</Text>}
        </Flex>
      ),
    },
    {
      id: "covers",
      header: "Covers",
      accessorFn: (row) => row.dnsNames?.join(" ") ?? "",
      cell: ({ row }) => <Text size="2" className="mono">{row.original.dnsNames?.join(", ") || "—"}</Text>,
      enableSorting: false,
    },
    {
      id: "state",
      header: "Status",
      accessorFn: (row) => certState(row, now),
      cell: ({ row }) => {
        const state = certState(row.original, now);
        return (
          <StatusBadge tone={state === "ok" ? "green" : state === "expiring" ? "amber" : "red"}>
            {state === "ok" ? "Valid" : state === "expiring" ? "Expires soon" : "Not issued yet"}
          </StatusBadge>
        );
      },
    },
    {
      id: "expires",
      header: "Expires in",
      accessorFn: (row) => row.notAfter ?? "",
      cell: ({ row }) =>
        row.original.notAfter ? (
          <Flex direction="column">
            <Text size="2">{formatDaysRemaining(daysUntil(row.original.notAfter, now))}</Text>
            <Text size="1" color="gray">{new Date(row.original.notAfter).toLocaleDateString()}</Text>
          </Flex>
        ) : (
          <Text color="gray">—</Text>
        ),
    },
  ];
}

// Every web address and certificate in the cluster. An app's own addresses are on its "How it
// fits" tab, but some addresses belong to no app (a namespace with no workloads), and
// certificates are the cluster's, so both are listed in full here.
export function ClusterAddresses({ report }: { report: Report }) {
  return (
    <DataTable
      data={report.rke2.ingresses ?? []}
      columns={addressColumns}
      getRowId={(row) => `${row.namespace}/${row.name}`}
      noun="addresses"
      empty="No web addresses were reported."
    />
  );
}

export function ClusterCertificates({ report }: { report: Report }) {
  const now = new Date(report.collectedAt);
  return (
    <DataTable
      data={report.rke2.certificates ?? []}
      columns={certificateColumns(now)}
      getRowId={(row) => `${row.namespace}/${row.name}`}
      noun="certificates"
      empty="No security certificates were reported."
      initialSorting={[{ id: "expires", desc: false }]}
    />
  );
}
