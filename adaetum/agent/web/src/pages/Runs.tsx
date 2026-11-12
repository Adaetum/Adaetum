import { Button, Callout, Flex, Heading, Skeleton } from "@radix-ui/themes";
import { ColumnDef } from "@tanstack/react-table";
import { CircleAlert, RefreshCw } from "lucide-react";
import { AppLocation, hrefFor } from "../app/routes";
import { useNow } from "../app/useReport";
import { DataTable } from "../components/DataTable";
import { PageHeader } from "../components/PageHeader";
import { RunDetailPanel } from "../components/runs/RunDetailPanel";
import { StartRunCard } from "../components/runs/StartRunCard";
import { StatusBadge } from "../components/StatusBadge";
import { formatAgo } from "../lib/format";
import { RUN_STATE, RunSnapshot, stepsDone, useRunsOverview } from "../lib/runsApi";

// Runs: the work this agent does, step by step. A run is one go at a procedure (bring a
// node up, unseal OpenBao). Every step checks the real system first, so a run that failed
// or was interrupted can simply be tried again.
//
// Like Settings and Cluster, this page is about the agent itself and does not wait for a
// host report.
export function Runs({ location }: { location: AppLocation }) {
  const { capabilities, procedures, runs, error, loading, refresh } = useRunsOverview();
  const now = new Date(useNow(5_000));
  const selected = location.params.get("run");

  const open = (id: string) => {
    window.location.hash = hrefFor({ path: "runs", params: { run: id } });
    refresh();
  };
  const close = () => {
    window.location.hash = hrefFor({ path: "runs" });
  };

  return (
    <>
      <PageHeader
        title="Runs"
        description="The work this agent does for you, step by step. Watch it, see why something stopped, and try it again."
        actions={
          <Button variant="soft" color="gray" onClick={refresh} loading={loading}>
            <RefreshCw size={14} aria-hidden /> Refresh
          </Button>
        }
      />
      {error && !capabilities ? (
        <Callout.Root color="red" role="alert">
          <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
          <Callout.Text>Couldn't read the runs: {error}.</Callout.Text>
        </Callout.Root>
      ) : !capabilities ? (
        <Flex direction="column" gap="4" aria-busy="true" aria-label="Loading">
          <Skeleton height="120px" />
          <Skeleton height="160px" />
        </Flex>
      ) : (
        <Flex direction="column" gap="5">
          {selected && <RunDetailPanel key={selected} id={selected} procedures={procedures} now={now} onClose={close} onChanged={refresh} />}
          <Flex direction="column" gap="2">
            <Heading size="3" as="h2">Start a run</Heading>
            <StartRunCard capabilities={capabilities} procedures={procedures} onStarted={open} />
          </Flex>
          <Flex direction="column" gap="2">
            <Heading size="3" as="h2">Recent runs</Heading>
            <DataTable
              data={runs}
              columns={columns(now)}
              getRowId={(run) => run.id}
              noun="runs"
              pageSize={10}
              empty="Nothing has run yet."
            />
          </Flex>
        </Flex>
      )}
    </>
  );
}

function columns(now: Date): ColumnDef<RunSnapshot>[] {
  return [
    {
      header: "Procedure",
      accessorKey: "procedure",
      cell: ({ row }) => (
        <a href={hrefFor({ path: "runs", params: { run: row.original.id } })}>{row.original.procedure}</a>
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
