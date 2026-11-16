import { Badge, Card, Flex, Text } from "@radix-ui/themes";
import { AppCard } from "../lib/apps";
import { useAbout } from "../lib/aboutApi";
import { useShowAbout } from "../lib/preferences";

// The "About this app" card on an app's Overview. Every field is shown as text. With no
// description in the repository it shows a placeholder that says how to add one, so the
// gap is visible instead of the card quietly missing.
export function AboutCard({ card }: { card: AppCard }) {
  const enabled = useShowAbout();
  const names = [...new Set([...card.deployments.map((deployment) => deployment.name), card.name])];
  const state = useAbout(names, enabled);
  if (!enabled || state.loading) return null;
  const { about, problem } = state.result;

  if (!about) {
    return (
      <Card variant="surface" style={{ borderStyle: "dashed" }}>
        <Flex direction="column" gap="1">
          <Text size="2" weight="bold">About this app</Text>
          <Text size="2" color={problem ? "red" : "gray"}>
            {problem ?? "Nothing has been written about this app yet."}
          </Text>
          {!problem && (
            <Text size="1" color="gray">
              Run the describe-pod-app skill in your cluster repository to draft it, or write
              <code> {card.name}.about.yaml </code> next to the app’s definition in <code>pods/</code>. It appears here once merged.
            </Text>
          )}
        </Flex>
      </Card>
    );
  }

  return (
    <Card variant="surface">
      <Flex direction="column" gap="3">
        <Flex justify="between" align="center" gap="2" wrap="wrap">
          <Text size="2" weight="bold">About this app</Text>
          <Badge variant="soft" color="gray">
            {about.written.by === "assistant" ? "Drafted by an assistant" : "Written by a person"} · {about.written.date}
            {about.written.version ? ` · for version ${about.written.version}` : ""}
          </Badge>
        </Flex>
        <Text size="2">{about.summary}</Text>
        {about.facts && about.facts.length > 0 && (
          <dl style={{ margin: 0, display: "grid", gridTemplateColumns: "max-content 1fr", gap: "6px 16px" }}>
            {about.facts.map((fact) => (
              <div key={fact.label} style={{ display: "contents" }}>
                <Text as="span" size="2" color="gray">{fact.label}</Text>
                <Text as="span" size="2">{fact.value}</Text>
              </div>
            ))}
          </dl>
        )}
        {about.watch && about.watch.length > 0 && (
          <Flex direction="column" gap="1">
            <Text size="2" weight="medium">Worth watching</Text>
            <ul style={{ margin: 0, paddingLeft: 18 }}>
              {about.watch.map((item) => <li key={item}><Text size="2">{item}</Text></li>)}
            </ul>
          </Flex>
        )}
      </Flex>
    </Card>
  );
}
