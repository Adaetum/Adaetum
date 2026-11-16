import { Callout, Flex, Heading, Text } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { CircleAlert } from "lucide-react";
import { DataTable } from "../DataTable";
import { StatusBadge } from "../StatusBadge";
import { formatAgo } from "../../lib/format";
import { RUN_STATE, RunSnapshot, ServerRuns, stepsDone } from "../../lib/runsApi";

type Row = RunSnapshot & { server: string };

// FleetRuns lists the runs on every server, read from each server's own agent when the
// page opened. They are shown, not managed: to start, retry or stop one, open that server's
// agent, because a run is that machine's own work.
export function FleetRuns({ servers, now }: { servers: ServerRuns[]; now: Date }) {
  const rows: Row[] = servers
    .flatMap((server) => server.runs.map((run) => ({ ...run, server: server.hostname })))
    .sort((a, b) => b.started.localeCompare(a.started));
  const unreadable = servers.filter((server) => server.error);

  return (
    <Flex direction="column" gap="3">
      <Heading size="3" as="h2">Runs on your servers</Heading>
      <Text size="2" color="gray">
        Read from each server's own agent just now. To start, retry or stop a run, open that server's agent.
      </Text>
      {unreadable.map((server) => (
        <Callout.Root key={server.hostId} color="amber" role="status">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text><strong>{server.hostname}</strong>: {server.error}</Callout.Text>
        </Callout.Root>
      ))}
      <DataTable
        data={rows}
        columns={columns(now)}
        getRowId={(run) => `${run.server}/${run.id}`}
        noun="runs"
        pageSize={10}
        empty={servers.length === 0 ? "No servers have joined this cluster yet." : "Nothing has run on these servers yet."}
      />
    </Flex>
  );
}

function columns(now: Date): ColumnDef<Row>[] {
  return [
    { header: "Server", accessorKey: "server" },
    { header: "Procedure", accessorKey: "procedure" },
    {
      header: "State",
      accessorFn: (run) => RUN_STATE[run.state].label,
      cell: ({ row }) => {
        const state = RUN_STATE[row.original.state];
        return <StatusBadge tone={state.tone}>{state.label}</StatusBadge>;
      },
    },
    {
      header: "Progress",
      accessorFn: (run) => stepsDone(run),
      cell: ({ row }) => `${stepsDone(row.original)} of ${row.original.steps.length} steps`,
    },
    { header: "Started", accessorKey: "started", cell: ({ row }) => formatAgo(row.original.started, now) },
    { header: "By", accessorFn: (run) => run.startedBy ?? "", cell: ({ row }) => row.original.startedBy || "—" },
  ];
}
