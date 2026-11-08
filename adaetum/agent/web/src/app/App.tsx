import { Button, Callout, Card, Code, Flex, Heading, Skeleton, Text } from "@radix-ui/themes";
import { CircleAlert, RefreshCw, ServerOff } from "lucide-react";
import { useEffect } from "react";
import { Report, SUPPORTED_SCHEMA_VERSION } from "../api";
import { useToken } from "../lib/auth";
import { summarizeAttention } from "../lib/attention";
import { Automation } from "../pages/Automation";
import { Cluster } from "../pages/Cluster";
import { AssistantProvider } from "../components/assistant/AssistantProvider";
import { Explore } from "../pages/Explore";
import { Login } from "../pages/Login";
import { Overview } from "../pages/Overview";
import { Settings } from "../pages/Settings";
import { Workloads } from "../pages/Workloads";
import { AppLocation, useLocation } from "./routes";
import { Shell } from "./Shell";
import { useReport } from "./useReport";

const REFRESH_INTERVAL_MS = 30_000;

export function App() {
  const { report, error, refreshing, updatedAt, authRequired, refresh } = useReport(REFRESH_INTERVAL_MS);
  const token = useToken();
  // The cluster agent serves this interface but has no host report (404).
  const clusterOnly = error?.includes("HTTP 404") ?? false;
  const location = useLocation();

  // A new page should start at the top, not at the previous page's scroll offset.
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [location.path]);

  // Surface the issue count in the tab title so a pinned tab works as a badge.
  useEffect(() => {
    const issues = report ? summarizeAttention(report).length : 0;
    document.title = issues > 0 ? `(${issues}) Adaetum` : "Adaetum";
  }, [report]);

  return (
    <AssistantProvider>
    <Shell
      report={report}
      error={error}
      refreshing={refreshing}
      updatedAt={updatedAt}
      refresh={refresh}
      location={location}
      intervalMs={REFRESH_INTERVAL_MS}
      locked={authRequired}
      clusterOnly={clusterOnly}
    >
      {authRequired ? (
        <Login rejected={token !== ""} onSignedIn={refresh} />
      ) : location.path === "cluster" ? (
        // The cluster view comes from the cluster agent, which has no host report, so
        // it must not wait for one. A host report, when there is one, adds "This server".
        <Cluster key={location.path} report={report} location={location} />
      ) : location.path === "explore" ? (
        <Explore key={location.path} />
      ) : location.path === "settings" ? (
        // Settings are the agent's own (assistant, security), so like the cluster view they
        // must not wait for a host report.
        <Settings key={location.path} location={location} />
      ) : report ? (
        <Page report={report} location={location} refresh={refresh} />
      ) : clusterOnly ? (
        <ClusterAgentOnly />
      ) : error ? (
        <Unreachable error={error} onRetry={refresh} />
      ) : (
        <Loading />
      )}
    </Shell>
    </AssistantProvider>
  );
}

function Page({ report, location, refresh }: { report: Report; location: AppLocation; refresh: () => void }) {
  if (report.schemaVersion !== SUPPORTED_SCHEMA_VERSION) {
    return (
      <Callout.Root color="red" role="alert">
        <Callout.Icon><CircleAlert size={16} /></Callout.Icon>
        <Callout.Text>
          The agent reports schema version {report.schemaVersion}, but this UI supports {SUPPORTED_SCHEMA_VERSION}. Update the UI or the agent so they match.
        </Callout.Text>
      </Callout.Root>
    );
  }

  // Keying on the full location resets a page's filters when a link such as
  // "unhealthy pods" navigates within the same route with different parameters.
  const key = `${location.path}?${location.params}`;
  switch (location.path) {
    case "workloads":
      // Not keyed on the address: moving between an app's tabs must not reset the list's filters.
      return <Workloads key="workloads" report={report} location={location} refresh={refresh} />;
    case "cluster":
      return <Cluster key={key} report={report} location={location} />;
    case "automation":
      return <Automation key={key} location={location} />;
    default:
      return <Overview key={key} report={report} />;
  }
}

// The cluster agent serves this same interface but has no host report of its own:
// host details live on each host's own agent. Say so, and point at the page that
// does work here.
function ClusterAgentOnly() {
  return (
    <Card size="4" style={{ maxWidth: 560, margin: "10vh auto 0" }}>
      <Flex direction="column" gap="3" py="2">
        <Heading size="5" as="h1">This is the cluster agent</Heading>
        <Text color="gray">
          It collects reports from every server's own agent, so it has no details of a single server. Open a server's own agent for
          those.
        </Text>
        <div>
          <Button asChild>
            <a href="#/cluster">Go to the cluster</a>
          </Button>
        </div>
      </Flex>
    </Card>
  );
}

function Loading() {
  return (
    <Flex direction="column" gap="4" aria-busy="true" aria-label="Loading">
      <Skeleton height="32px" width="200px" />
      <Skeleton height="96px" />
      <div className="tile-grid">
        {Array.from({ length: 6 }, (_, index) => (
          <Skeleton key={index} height="110px" />
        ))}
      </div>
    </Flex>
  );
}

function Unreachable({ error, onRetry }: { error: string; onRetry: () => void }) {
  return (
    <Card size="4" style={{ maxWidth: 560, margin: "10vh auto 0" }}>
      <Flex direction="column" align="center" gap="3" py="4">
        <ServerOff size={32} color="var(--red-11)" aria-hidden />
        <Heading size="5" as="h1">Can't reach the agent</Heading>
        <Text color="gray" align="center">
          {error}. Check that the agent is running, for example <Code>adaetum-agent serve</Code>, then try again.
        </Text>
        <Button onClick={onRetry}>
          <RefreshCw size={14} /> Try again
        </Button>
      </Flex>
    </Card>
  );
}
