import { Callout, Code, Flex, Text } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { Info } from "lucide-react";
import { useState } from "react";
import { ArgoApp, Report } from "../api";
import { DataTable } from "./DataTable";
import { Toggle } from "./Filters";
import { StatusBadge } from "./StatusBadge";
import { argoConverged } from "../lib/status";
import { healthState, syncState } from "../lib/vocabulary";

const columns: ColumnDef<ArgoApp, any>[] = [
  {
    id: "name",
    header: "Deployment",
    accessorFn: (row) => row.name,
    cell: ({ row }) => (
      <Flex direction="column" gap="1">
        <Text size="2" weight="medium">{row.original.name}</Text>
        {row.original.message && <Text size="1" color="red">{row.original.message}</Text>}
      </Flex>
    ),
  },
  {
    id: "sync",
    header: "Matches repository",
    // Worst first when sorted descending.
    accessorFn: (row) => syncState(row.sync).rank,
    cell: ({ row }) => {
      const state = syncState(row.original.sync);
      return <StatusBadge tone={state.tone} title={row.original.sync}>{state.label}</StatusBadge>;
    },
  },
  {
    id: "health",
    header: "Health",
    accessorFn: (row) => healthState(row.health).rank,
    cell: ({ row }) => {
      const state = healthState(row.original.health);
      return <StatusBadge tone={state.tone} title={row.original.health}>{state.label}</StatusBadge>;
    },
  },
  {
    id: "version",
    header: "Version",
    accessorFn: (row) => row.revision ?? "",
    cell: ({ row }) => (row.original.revision ? <Code size="1" variant="ghost">{row.original.revision}</Code> : <Text color="gray">—</Text>),
  },
];

// Every deployment (Argo CD application) in the cluster, including the ones that belong to no
// app, such as the platform's own. An app's own deployments are on its Deployments tab.
export function ClusterDeployments({ report, initialAttention = false }: { report: Report; initialAttention?: boolean }) {
  const [onlyAttention, setOnlyAttention] = useState(initialAttention);
  const deployments = report.rke2.argoApps ?? [];
  const visible = deployments.filter((item) => !onlyAttention || !argoConverged(item));

  return deployments.length === 0 ? (
    <Callout.Root color="gray">
      <Callout.Icon><Info size={16} /></Callout.Icon>
      <Callout.Text>No deployments were reported. Deployment tracking may not be set up on this cluster yet.</Callout.Text>
    </Callout.Root>
  ) : (
    <DataTable
      data={visible}
      columns={columns}
      getRowId={(row) => `${row.namespace}/${row.name}`}
      noun="deployments"
      toolbar={<Toggle label="Needs attention only" checked={onlyAttention} onChange={setOnlyAttention} />}
      empty="Every deployment is up to date and healthy."
      initialSorting={[{ id: "sync", desc: true }]}
    />
  );
}
