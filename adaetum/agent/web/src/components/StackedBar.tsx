import { Box, Flex, Text } from "@radix-ui/themes";
import { formatBytes } from "../lib/format";

export interface BarSegment {
  label: string;
  bytes: number;
  // CSS color for the swatch and bar. "hatched" draws diagonal stripes, used
  // for space that is held back rather than filled.
  color: string;
  hatched?: boolean;
  // One plain sentence saying what this number means.
  description?: string;
}

interface StackedBarProps {
  title: string;
  totalBytes: number;
  segments: BarSegment[];
}

// StackedBar shows one whole divided into named parts, with a legend that
// carries the exact values. The legend, not the colors, is the primary
// information, so the bar is hidden from assistive technology.
export function StackedBar({ title, totalBytes, segments }: StackedBarProps) {
  const percent = (bytes: number) => (totalBytes > 0 ? Math.round((bytes / totalBytes) * 100) : 0);

  return (
    <Flex direction="column" gap="3">
      <Text size="2" weight="medium">{title}</Text>
      <div className="stacked-bar" aria-hidden>
        {segments.map((segment) => (
          <div
            key={segment.label}
            className={segment.hatched ? "stacked-segment hatched" : "stacked-segment"}
            style={{ width: `${totalBytes > 0 ? (segment.bytes / totalBytes) * 100 : 0}%`, background: segment.hatched ? undefined : segment.color, color: segment.color }}
          />
        ))}
      </div>
      <ul className="legend" role="list">
        {segments.map((segment) => (
          <Flex key={segment.label} gap="3" align="start" className="legend-row" asChild>
            <li>
              <span
                className={segment.hatched ? "swatch hatched" : "swatch"}
                style={{ background: segment.hatched ? undefined : segment.color, color: segment.color }}
                aria-hidden
              />
              <Box style={{ flex: 1, minWidth: 0 }}>
                <Text size="2" weight="medium" as="div">{segment.label}</Text>
                {segment.description && <Text size="1" color="gray" as="div">{segment.description}</Text>}
              </Box>
              <div style={{ textAlign: "right" }}>
                <Text size="2" weight="medium" as="div">{formatBytes(segment.bytes)}</Text>
                <Text size="1" color="gray" as="div">{percent(segment.bytes)}%</Text>
              </div>
            </li>
          </Flex>
        ))}
      </ul>
    </Flex>
  );
}
