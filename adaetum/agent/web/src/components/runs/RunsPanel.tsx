import { Button, Callout, Flex, Heading, Skeleton } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { CircleAlert, RefreshCw } from "lucide-react";
import { AppLocation, hrefFor } from "../../app/routes";
import { useNow } from "../../app/useReport";
import { formatAgo } from "../../lib/format";
import { RUN_STATE, RunSnapshot, stepsDone, useFleetRuns, useRunsOverview } from "../../lib/runsApi";
import { DataTable } from "../DataTable";
import { StatusBadge } from "../StatusBadge";
import { FleetRuns } from "./FleetRuns";
import { RunDetailPanel } from "./RunDetailPanel";
import { StartRunCard } from "./StartRunCard";

// The work the agents do, step by step, shown inside the Automation page. A run is one go at a
// procedure (bring a node up, unseal OpenBao). Every step checks the real system first, so a run
// that failed or was interrupted can simply be tried again.
//
// A host agent shows its own runs and can start one. The cluster agent has none of its own: it
// reads every server's runs from that server's agent when this opens (and on Refresh).
export function RunsPanel({ location }: { location: AppLocation }) {
  const { capabilities, procedures, runs, error, loading, refresh } = useRunsOverview();
  const fleet = useFleetRuns(capabilities?.available === false);
  const now = new Date(useNow(5_000));
  const selected = location.params.get("run");

  const open = (id: string) => {
    window.location.hash = hrefFor({ path: "automation", params: { run: id } });
    refresh();
  };
  const refreshAll = () => {
    refresh();
    fleet.refresh();
  };
  const close = () => {
    window.location.hash = hrefFor({ path: "automation" });
  };

  if (error && !capabilities) {
    return (
      <Callout.Root color="red" role="alert">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>Couldn't read the runs: {error}.</Callout.Text>
      </Callout.Root>
    );
  }
  if (!capabilities) {
    return (
      <Flex direction="column" gap="4" aria-busy="true" aria-label="Loading">
        <Skeleton height="120px" />
      </Flex>
    );
  }
  return (
    <Flex direction="column" gap="4">
      <Flex justify="end">
        <Button variant="soft" color="gray" size="1" onClick={refreshAll} loading={loading || fleet.loading}>
          <RefreshCw size={14} aria-hidden /> Refresh runs
        </Button>
      </Flex>
      {fleet.servers ? (
        <FleetRuns servers={fleet.servers} now={now} />
      ) : fleet.error ? (
        <Callout.Root color="red" role="alert">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>Couldn't read the servers' runs: {fleet.error}.</Callout.Text>
        </Callout.Root>
      ) : null}
      {!fleet.servers && selected && (
        <RunDetailPanel key={selected} id={selected} procedures={procedures} now={now} onClose={close} onChanged={refresh} />
      )}
      {!fleet.servers && (
        <>
          <Flex direction="column" gap="2">
            <Heading size="3" as="h3">Start a run</Heading>
            <StartRunCard capabilities={capabilities} procedures={procedures} onStarted={open} />
          </Flex>
          <Flex direction="column" gap="2">
            <Heading size="3" as="h3">Recent runs</Heading>
            <DataTable
              data={runs}
              columns={columns(now)}
              getRowId={(run) => run.id}
              noun="runs"
              pageSize={10}
              empty="Nothing has run yet."
            />
          </Flex>
        </>
      )}
    </Flex>
  );
}

function columns(now: Date): ColumnDef<RunSnapshot>[] {
  return [
    {
      header: "Procedure",
      accessorKey: "procedure",
      cell: ({ row }) => (
        <a href={hrefFor({ path: "automation", params: { run: row.original.id } })}>{row.original.procedure}</a>
      ),
    },
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
    {
      header: "Started",
      accessorKey: "started",
      cell: ({ row }) => formatAgo(row.original.started, now),
    },
    {
      header: "By",
      accessorFn: (run) => run.startedBy ?? "",
      cell: ({ row }) => row.original.startedBy || "—",
    },
  ];
}
