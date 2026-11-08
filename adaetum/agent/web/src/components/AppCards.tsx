import { Badge, Card, Flex, Text } from "@radix-ui/themes";
import { Autoscaler } from "../api";
import { AppCard } from "../lib/apps";
import { formatBytes } from "../lib/format";
import { History } from "../lib/historyApi";
import { sizingFor } from "../lib/sizing";
import { argoConverged } from "../lib/status";
import { AppLogo } from "./AppLogo";
import { Radar, SectionChips } from "./Scorecard";
import { StatusBadge } from "./StatusBadge";

// One card per app. To a person plex is one thing; whether it is made of one component or
// five, runs as one instance or three, or is delivered by a deployment is the detail
// behind the card, not the card itself.
// Give `hrefOf` to make each card a link to the app's own page, or `onOpen` for a quick view.
export function AppCards({ apps, autoscalers, history, onOpen, hrefOf, empty }: { apps: AppCard[]; autoscalers: Autoscaler[]; history: History | null; onOpen?: (card: AppCard) => void; hrefOf?: (card: AppCard) => string; empty: string }) {
  if (apps.length === 0) return <Text color="gray" as="p">{empty}</Text>;
  // Radix's Card needs one real element to become: a link to the app's page, or a button.
  const wrap = (card: AppCard, body: React.ReactNode) => {
    const label = `${card.name}: ${card.healthy ? "running" : "needs attention"}. Show details.`;
    return hrefOf ? <a href={hrefOf(card)} aria-label={label}>{body}</a> : <button type="button" onClick={() => onOpen?.(card)} aria-label={label}>{body}</button>;
  };
  return (
    <ul className="app-grid">
      {apps.map((item) => {
        const sizing = sizingFor(item, autoscalers, history);
        const measured = item.volumes.filter((volume) => volume.usedBytes !== undefined && volume.capacityBytes);
        const used = measured.reduce((sum, volume) => sum + (volume.usedBytes ?? 0), 0);
        const capacity = measured.reduce((sum, volume) => sum + (volume.capacityBytes ?? 0), 0);
        const behind = item.deployments.some((deployment) => !argoConverged(deployment));
        return (
          <li key={`${item.namespace}/${item.name}`}>
            <Card asChild size="2" className="app-card">
              {wrap(item, (
                <Flex gap="3" align="start">
                  <AppLogo source={{ name: item.name, chart: item.chart, repo: item.chartRepo, parts: item.parts }} label={item.name} />
                  <Flex direction="column" gap="1" style={{ minWidth: 0, flex: 1 }}>
                    <Flex justify="between" align="center" gap="2">
                      <Flex gap="2" align="center" style={{ minWidth: 0 }}>
                        <Text size="3" weight="medium" truncate>{item.name}</Text>
                        {item.system && <Badge variant="outline" color="gray">System</Badge>}
                      </Flex>
                      <StatusBadge tone={item.healthy ? "green" : "red"}>{item.healthy ? "Running" : "Needs attention"}</StatusBadge>
                    </Flex>
                    <Text size="2" color={item.healthy ? "gray" : "red"}>
                      {item.healthy ? (item.instances.length === 0 && item.volumes.length === 0 ? "Nothing is running right now." : "Running normally.") : item.problem}
                    </Text>
                    {sizing && (
                      <Flex gap="3" align="center" mt="1">
                        <Radar sizing={sizing} />
                        <Flex direction="column" gap="1" style={{ minWidth: 0, flex: 1 }}>
                          <Text size="2" weight="bold" color={sizing.tone === "gray" ? undefined : sizing.tone}>Setup {sizing.points}/100 · {sizing.grade}</Text>
                          <SectionChips sizing={sizing} />
                          {sizing.top[0] && <Text size="1" color={sizing.grade === "Good" ? "gray" : sizing.tone}>{sizing.top[0]}</Text>}
                        </Flex>
                      </Flex>
                    )}
                    <Flex gap="2" wrap="wrap" mt="1">
                      {capacity > 0 && <Badge variant="outline" color="gray">{formatBytes(used)} of {formatBytes(capacity)} stored</Badge>}
                      {behind && <Badge variant="outline" color="amber">Update pending</Badge>}
                      {item.restarts > 0 && <Badge variant="outline" color="amber">{item.restarts} {item.restarts === 1 ? "restart" : "restarts"}</Badge>}
                    </Flex>
                  </Flex>
                </Flex>
              ))}
            </Card>
          </li>
        );
      })}
    </ul>
  );
}
