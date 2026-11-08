import { Callout, Card, Flex, SegmentedControl, Switch, Tabs, Text } from "@radix-ui/themes";
import { Info } from "lucide-react";
import { AppLocation, hrefFor } from "../app/routes";
import { PageHeader, SettingsSection } from "../components/PageHeader";
import { AssistantMode, setAdvancedUser, setAssistantMode, useAdvancedUser, useAssistantMode } from "../lib/preferences";
import { AiModel } from "./AiModel";
import { Embedding } from "./Embedding";
import { Memory } from "./Memory";
import { People } from "./People";
import { Profile } from "./Profile";
import { Security } from "./Security";

const TABS = [
  { id: "general", label: "General" },
  { id: "profile", label: "Profile" },
  { id: "people", label: "People" },
  { id: "ai-model", label: "AI model" },
  { id: "memory", label: "Memory" },
  { id: "embedding", label: "Embedding" },
  { id: "security", label: "Security" },
] as const;
type TabId = (typeof TABS)[number]["id"];

// Every setting in one place. General holds this browser's preferences, Profile is about you and People about who can sign in; the rest are the
// pages that used to sit in the sidebar by themselves, unchanged and shown as sections.
export function Settings({ location }: { location: AppLocation }) {
  const requested = location.params.get("tab");
  const tab: TabId = TABS.find((item) => item.id === requested)?.id ?? "general";
  return (
    <>
      <PageHeader title="Settings" description="How the dashboard behaves, and how the assistant, its memory and the agent's powers are set up." />
      <Tabs.Root value={tab} onValueChange={(next) => { window.location.hash = hrefFor({ path: "settings", params: { tab: next } }); }}>
        <Tabs.List mb="4">
          {TABS.map((item) => <Tabs.Trigger key={item.id} value={item.id}>{item.label}</Tabs.Trigger>)}
        </Tabs.List>
        {/* Only the open section is mounted, so a section's own loading starts when it is opened. */}
        <SettingsSection.Provider value>
          {tab === "general" && <General />}
          {tab === "profile" && <Profile />}
          {tab === "people" && <People />}
          {tab === "ai-model" && <AiModel />}
          {tab === "memory" && <Memory />}
          {tab === "embedding" && <Embedding />}
          {tab === "security" && <Security />}
        </SettingsSection.Provider>
      </Tabs.Root>
    </>
  );
}

function General() {
  const advanced = useAdvancedUser();
  const mode = useAssistantMode();
  return (
    <Flex direction="column" gap="4" style={{ maxWidth: 640 }}>
      <Card size="3">
        <Flex justify="between" align="start" gap="4">
          <Flex direction="column" gap="1">
            <Text size="3" weight="medium" as="label" htmlFor="advanced-user">Advanced user mode</Text>
            <Text size="2" color="gray">
              Keeps the full detail open on every page instead of tucked away under an Advanced line: the tables of volumes,
              services, deployments and copies. Nothing is hidden from anyone either way. This only decides whether it is
              open when you arrive.
            </Text>
          </Flex>
          <Switch id="advanced-user" checked={advanced} onCheckedChange={setAdvancedUser} aria-label="Advanced user mode" />
        </Flex>
      </Card>
      <Card size="3">
        <Flex justify="between" align="start" gap="4" wrap="wrap">
          <Flex direction="column" gap="1" style={{ maxWidth: 380 }}>
            <Text size="3" weight="medium" id="assistant-mode-label">Assistant panel</Text>
            <Text size="2" color="gray">
              “Make room” keeps the page beside the assistant, so you can keep clicking around while it works. “Over the page”
              lays it on top, for when you want to focus on the conversation. On a narrow screen it always goes over the page.
            </Text>
          </Flex>
          <SegmentedControl.Root value={mode} onValueChange={(next) => setAssistantMode(next as AssistantMode)} aria-labelledby="assistant-mode-label">
            <SegmentedControl.Item value="split">Make room</SegmentedControl.Item>
            <SegmentedControl.Item value="over">Over the page</SegmentedControl.Item>
          </SegmentedControl.Root>
        </Flex>
      </Card>
      <Callout.Root color="gray" size="1">
        <Callout.Icon><Info size={14} /></Callout.Icon>
        <Callout.Text>These are remembered in this browser only. It changes how the dashboard looks, not what the agent does.</Callout.Text>
      </Callout.Root>
    </Flex>
  );
}
