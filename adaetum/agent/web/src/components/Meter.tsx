import { Flex, Progress, Text } from "@radix-ui/themes";
import { toneForUsage } from "../lib/status";

interface MeterProps {
  label: string;
  fraction: number;
  detail: string;
}

export function Meter({ label, fraction, detail }: MeterProps) {
  const clamped = Math.min(Math.max(fraction, 0), 1);
  const percent = Math.round(clamped * 100);
  return (
    <Flex direction="column" gap="1">
      <Flex justify="between" align="baseline" gap="3">
        <Text size="2" weight="medium">{label}</Text>
        <Text size="2" color="gray" align="right">{detail}</Text>
      </Flex>
      <Progress value={percent} color={toneForUsage(clamped)} size="2" aria-label={`${label} ${percent}%`} />
    </Flex>
  );
}
