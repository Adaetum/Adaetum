import { Flex, Text } from "@radix-ui/themes";
import { ReactNode } from "react";
import { useAdvancedUser } from "../lib/preferences";

// The detail the dedicated pages used to show, under a line. `open` starts it unfolded, for a
// link that points at what is inside. A normal user sees it closed
// so the page reads at a glance; an advanced user (Settings > General) sees it open and
// fixed in place, since they want it every time.
export function Advanced({ children, label = "Advanced", open = false }: { children: ReactNode; label?: string; open?: boolean }) {
  const always = useAdvancedUser();
  const body = <Flex direction="column" gap="5" pt="3">{children}</Flex>;
  return always ? (
    <section className="advanced" aria-label={label}>
      <Text size="2" weight="bold">{label}</Text>
      {body}
    </section>
  ) : (
    <details className="advanced" open={open || undefined}>
      <summary><Text size="2" weight="bold">{label}</Text></summary>
      {body}
    </details>
  );
}

// A titled block inside Advanced.
export function AdvancedBlock({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <Flex direction="column" gap="2">
      <Text size="2" weight="medium">{title}</Text>
      {note && <Text size="1" color="gray">{note}</Text>}
      <div className="advanced-scroll">{children}</div>
    </Flex>
  );
}
