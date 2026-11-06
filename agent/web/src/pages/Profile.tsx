import { Badge, Button, Card, Flex, SegmentedControl, Text } from "@radix-ui/themes";
import { LogOut, Monitor, Moon, Sun } from "lucide-react";
import { ThemeMode, useThemeMode } from "../app/theme";
import { PageHeader } from "../components/PageHeader";
import { signOutEverything, useMe } from "../lib/session";

const ROLE_TEXT: Record<string, string> = {
  owner: "Owner: can change everything, including who has an account.",
  admin: "Admin: can change settings and run changes, but not manage people.",
  member: "Member: can look at everything but not change it.",
};

// Profile is about you: who the server thinks you are, how you signed in, and the
// preferences that follow you rather than the server (the colour theme).
export function Profile() {
  const { me } = useMe();
  const { mode, setMode } = useThemeMode();
  return (
    <>
      <PageHeader title="Profile" description="Who you are on this server, and how it looks for you." />
      <Flex direction="column" gap="4" style={{ maxWidth: 640 }}>
        <Card size="3">
          <Flex direction="column" gap="2">
            {!me ? (
              <Text size="2" color="gray">This server has no accounts, so there is no profile to show. You are using it with the access token, or without a login on this machine.</Text>
            ) : (
              <>
                <Flex gap="2" align="center" wrap="wrap">
                  <Text size="4" weight="bold">{me.viaToken ? "Access token" : me.name || me.email}</Text>
                  <Badge color={me.role === "owner" ? "indigo" : me.role === "admin" ? "amber" : "gray"} variant="soft">{me.viaToken ? "Owner (token)" : me.role}</Badge>
                </Flex>
                {me.email && <Text size="2" color="gray">{me.email}</Text>}
                <Text size="2">
                  {me.viaToken
                    ? "You are signed in with the server's access token, the emergency way in. Actions you take are recorded as \"operator\", not under a name."
                    : ROLE_TEXT[me.role] ?? ""}
                </Text>
              </>
            )}
          </Flex>
        </Card>

        <Card size="3">
          <Flex justify="between" align="center" gap="4" wrap="wrap">
            <Flex direction="column" gap="1">
              <Text size="3" weight="medium">Colour theme</Text>
              <Text size="2" color="gray">Remembered in this browser.</Text>
            </Flex>
            <SegmentedControl.Root size="2" value={mode} onValueChange={(next) => setMode(next as ThemeMode)} aria-label="Color theme">
              <SegmentedControl.Item value="light"><Sun size={14} aria-hidden /> Light</SegmentedControl.Item>
              <SegmentedControl.Item value="system"><Monitor size={14} aria-hidden /> Auto</SegmentedControl.Item>
              <SegmentedControl.Item value="dark"><Moon size={14} aria-hidden /> Dark</SegmentedControl.Item>
            </SegmentedControl.Root>
          </Flex>
        </Card>

        <Flex>
          <Button variant="soft" color="gray" onClick={() => void signOutEverything()}><LogOut size={14} aria-hidden /> Sign out</Button>
        </Flex>
      </Flex>
    </>
  );
}
