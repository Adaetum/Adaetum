import { Throbber } from "../components/Throbber";
import { Button, Callout, Flex, IconButton, SegmentedControl, Text, Tooltip } from "@radix-ui/themes";
import {
  Activity,
  LayoutDashboard,
  LayoutGrid,
  Monitor,
  Workflow,
  Moon,
  RefreshCw,
  Bot,
  Settings,
  Compass,
  LogOut,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  Sun,
  TriangleAlert,
} from "lucide-react";
import { ReactNode, useEffect, useState } from "react";
import { Report } from "../api";
import { useToken } from "../lib/auth";
import { useConnection } from "../lib/connection";
import { useAssistant } from "../components/assistant/AssistantProvider";
import { signOutEverything, useMe } from "../lib/session";
import { formatDuration } from "../lib/format";
import { AppLocation, hrefFor, ROUTES, RouteId } from "./routes";
import { ThemeMode, useThemeMode } from "./theme";
import { useNow } from "./useReport";

const SIDEBAR_KEY = "adaetum.sidebarCollapsed";

// Kept in this browser only, like the theme; storage can be blocked, so it is guarded.
function readCollapsed(): boolean {
  try {
    return window.localStorage.getItem(SIDEBAR_KEY) === "1";
  } catch {
    return false;
  }
}
function writeCollapsed(collapsed: boolean) {
  try {
    window.localStorage.setItem(SIDEBAR_KEY, collapsed ? "1" : "0");
  } catch {
    /* applies for this session only */
  }
}

const ICONS: Record<RouteId, ReactNode> = {
  overview: <LayoutDashboard size={18} />,
  workloads: <LayoutGrid size={18} />,
  automation: <Workflow size={18} />,
  cluster: <Network size={18} />,
  explore: <Compass size={18} />,
  settings: <Settings size={18} />,
};

interface ShellProps {
  report: Report | null;
  error: string | null;
  refreshing: boolean;
  updatedAt: number | null;
  refresh: () => void;
  location: AppLocation;
  intervalMs: number;
  // The agent is asking for a token: show only the brand, since nothing else is
  // available yet and a "Live" indicator would be misleading.
  locked?: boolean;
  // The agent answering is the cluster agent, which has no host report: its freshness
  // comes from the connection check instead.
  clusterOnly?: boolean;
  children: ReactNode;
}

export function Shell({ report, error, refreshing, updatedAt, refresh, location, intervalMs, locked = false, clusterOnly = false, children }: ShellProps) {
  const now = useNow();
  const assistant = useAssistant();
  const { me } = useMe();
  // Signed in with the token, or with an account: either has something to sign out of.
  const signedIn = useToken() !== "" || Boolean(me && !me.viaToken);
  const link = useConnection(intervalMs);
  const updated = clusterOnly ? link.checkedAt : updatedAt;
  const ageSeconds = updated ? Math.max(0, Math.floor((now - updated) / 1000)) : null;
  // Stale means two missed refreshes; an error is a failed one.
  const stale = ageSeconds !== null && ageSeconds * 1000 > intervalMs * 2;
  const offline = (!clusterOnly && Boolean(error)) || link.offline;
  // Reached a host, but the host cannot reach the pod: everything shown is this one
  // server. Amber, because it works but is not the whole picture.
  const hostOnly = link.connection?.role === "host" && link.connection.cluster === "unreachable";
  const updatedText = ageSeconds === null ? "Not updated yet" : `Updated ${formatDuration(ageSeconds)} ago`;
  // Which kind of agent this page is talking to, so "Live" says what is live.
  const where = link.connection?.role === "cluster" ? "Cluster" : link.connection?.role === "host" ? "Host" : null;
  const withWhere = (text: string) => (where ? `${where} · ${text.toLowerCase()}` : text);
  const connection = offline
    ? { color: "red" as const, label: "Offline", detail: "Can't reach the agent" }
    : hostOnly
      ? { color: "amber" as const, label: "Cluster unreachable", detail: `Host only · ${updatedText.toLowerCase()}` }
      : stale
        ? { color: "amber" as const, label: "Data is stale", detail: withWhere(updatedText) }
        : { color: "green" as const, label: "Live", detail: withWhere(updatedText) };
  // The sidebar shrinks to icons. It does so by itself while the assistant is docked beside
  // the page, to leave the page room, and goes back to the person's own choice when it closes. Pressing
  // the toggle while it is docked overrides that until it next docks or undocks.
  const [pinned, setPinned] = useState(readCollapsed);
  const [manual, setManual] = useState<boolean | null>(null);
  useEffect(() => setManual(null), [assistant.docked]);
  const collapsed = manual ?? (assistant.docked || pinned);
  const toggleSidebar = () => {
    const next = !collapsed;
    setManual(next);
    if (!assistant.docked) {
      setPinned(next);
      writeCollapsed(next);
    }
  };
  const refreshAll = () => {
    refresh();
    link.refresh();
  };

  return (
    <div className="shell" data-collapsed={collapsed}>
      <Throbber />
      <aside className="sidebar">
        <Flex align="center" gap="3" className="brand">
          <div className="brand-mark" aria-hidden>
            <Activity size={18} />
          </div>
          <Text size="4" weight="bold" className="label">Adaetum</Text>
        </Flex>

        {!locked && (
        <nav aria-label="Primary" className="nav">
          {ROUTES.map((route) => (
            <a
              key={route.id}
              href={hrefFor({ path: route.id })}
              className="nav-link"
              aria-current={location.path === route.id ? "page" : undefined}
              // With the labels hidden, the name is still what the link announces and shows on hover.
              title={collapsed ? route.label : undefined}
              aria-label={collapsed ? route.label : undefined}
            >
              {ICONS[route.id]}
              <span className="label">{route.label}</span>
            </a>
          ))}
        </nav>
        )}

        <Flex direction="column" gap="3" className="sidebar-footer">
          {!locked && (
          <Flex align="center" justify="between" gap="2">
            <Flex direction="column" title={collapsed ? `${connection.label}. ${connection.detail}` : undefined}>
              <Flex align="center" gap="2">
                <span className={`status-dot ${connection.color}`} aria-hidden />
                <Text size="2" weight="medium" className="label">{connection.label}</Text>
              </Flex>
              <Text size="1" color="gray" className="label">{connection.detail}</Text>
            </Flex>
            <Tooltip content="Refresh now">
              <IconButton size="1" variant="ghost" color="gray" aria-label="Refresh now" onClick={refreshAll} disabled={refreshing} className="label">
                <RefreshCw size={14} className={refreshing ? "spin" : undefined} />
              </IconButton>
            </Tooltip>
          </Flex>
          )}
          {!locked && (
            // The assistant is a drawer, not a page: it opens over whatever you are on.
            <Button size="2" variant={assistant.isOpen ? "solid" : "soft"} onClick={assistant.toggle} aria-pressed={assistant.isOpen} aria-label="Assistant" title={collapsed ? "Assistant" : undefined}>
              <Bot size={16} aria-hidden /> <span className="label">Assistant{assistant.busy && !assistant.isOpen ? " · working" : ""}</span>
            </Button>
          )}
          <div className="label"><ThemeSwitcher /></div>
          {signedIn && (
            // Only shown when a token is in use: an agent that asks for none has
            // nothing to sign out of.
            <Button size="1" variant="ghost" color="gray" onClick={() => void signOutEverything()}>
              <LogOut size={14} aria-hidden /> <span className="label">Sign out{me && !me.viaToken && (me.name || me.email) ? ` ${me.name || me.email}` : ""}</span>
            </Button>
          )}
          <Tooltip content={collapsed ? "Expand the sidebar" : "Collapse the sidebar"}>
            <IconButton size="1" variant="ghost" color="gray" className="sidebar-toggle" aria-label={collapsed ? "Expand the sidebar" : "Collapse the sidebar"} onClick={toggleSidebar}>
              {collapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
            </IconButton>
          </Tooltip>
        </Flex>
      </aside>

      <main className="content" id="main">
        {error && report && (
          // Sticky so it stays visible however far the page is scrolled.
          <Callout.Root color="amber" size="1" mb="4" role="status" className="stale-banner">
            <Callout.Icon><TriangleAlert size={16} /></Callout.Icon>
            <Callout.Text>
              Showing the last successful report. The agent could not be reached: {error}
            </Callout.Text>
          </Callout.Root>
        )}
        {children}
      </main>
    </div>
  );
}

function ThemeSwitcher() {
  const { mode, setMode } = useThemeMode();
  return (
    <SegmentedControl.Root size="1" value={mode} onValueChange={(next) => setMode(next as ThemeMode)} aria-label="Color theme">
      <SegmentedControl.Item value="light" aria-label="Light theme"><Sun size={14} /></SegmentedControl.Item>
      <SegmentedControl.Item value="system" aria-label="System theme"><Monitor size={14} /></SegmentedControl.Item>
      <SegmentedControl.Item value="dark" aria-label="Dark theme"><Moon size={14} /></SegmentedControl.Item>
    </SegmentedControl.Root>
  );
}
