import { Box, Card, Flex, Heading, Text } from "@radix-ui/themes";
import { ChevronRight } from "lucide-react";
import { ReactNode } from "react";
import { Destination, hrefFor } from "../app/routes";
import { Tone } from "../lib/status";

interface StatTileProps {
  icon: ReactNode;
  label: string;
  value: string;
  caption: string;
  tone: Tone;
  to: Destination;
}

// A tile answers one question at a glance and links to the view behind it.
export function StatTile({ icon, label, value, caption, tone, to }: StatTileProps) {
  return (
    <Card asChild size="2" className="tile">
      {/* No aria-label: the visible text already forms the link's name, and an
          overriding label that differs from it fails WCAG "label in name". */}
      <a href={hrefFor(to)}>
        <Flex direction="column" gap="3">
          <Flex justify="between" align="center">
            <Flex align="center" gap="2" style={{ color: `var(--${tone}-11)` }}>
              {icon}
              <Text size="2" weight="medium" color="gray">{label}</Text>
            </Flex>
            <ChevronRight size={16} className="tile-chevron" aria-hidden />
          </Flex>
          <Box>
            <Heading size="7" weight="medium">{value}</Heading>
            <Text size="2" style={{ color: `var(--${tone}-11)` }}>{caption}</Text>
          </Box>
        </Flex>
      </a>
    </Card>
  );
}
