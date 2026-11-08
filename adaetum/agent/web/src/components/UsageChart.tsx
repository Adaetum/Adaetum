import { Flex, Text } from "@radix-ui/themes";
import { HistoryPoint } from "../lib/historyApi";

// One resource of one workload over the recorded hours: the band between the usual use
// and the highest, with dashed lines for what it reserves (request) and what it is
// allowed (limit). The scale fits the USE, so a limit far above it is not drawn but
// stated: showing it would flatten the line, and the gap is exactly the finding.
export function UsageChart({
  title, points, pick, request, limit, format, limitLabel = "allowed",
}: {
  title: string;
  points: HistoryPoint[];
  pick: (point: HistoryPoint) => { peak: number; avg: number };
  request?: number;
  limit?: number;
  format: (value: number) => string;
  limitLabel?: string;
}) {
  const width = 300;
  const height = 64;
  if (points.length === 0) return null;
  const values = points.map(pick);
  const highest = Math.max(...values.map((value) => value.peak));
  const scaleMax = Math.max(highest, request ?? 0, 1) * 1.15;
  const limitShown = limit !== undefined && limit <= scaleMax;
  const top = limitShown ? Math.max(scaleMax, limit * 1.05) : scaleMax;
  const times = points.map((point) => new Date(point.hour).getTime());
  const start = Math.min(...times);
  const span = Math.max(Math.max(...times) - start, 1);
  const x = (index: number) => (points.length === 1 ? width / 2 : ((times[index] - start) / span) * width);
  const y = (value: number) => height - (value / top) * height;
  const line = (key: "peak" | "avg") => values.map((value, index) => `${x(index).toFixed(1)},${y(value[key]).toFixed(1)}`);
  const band = [...line("peak"), ...line("avg").reverse()].join(" ");

  return (
    <Flex direction="column" gap="1" style={{ minWidth: 0, flex: 1 }}>
      <Text size="1" weight="medium">{title}</Text>
      <svg viewBox={`0 0 ${width} ${height}`} width="100%" height={height} preserveAspectRatio="none" role="img"
        aria-label={`${title} over the recorded hours. Highest ${format(highest)}.`}>
        <polygon points={band} className="chart-band" />
        <polyline points={line("avg").join(" ")} className="chart-avg" fill="none" />
        {request !== undefined && request <= top && <line x1="0" x2={width} y1={y(request)} y2={y(request)} className="chart-request" />}
        {limitShown && <line x1="0" x2={width} y1={y(limit)} y2={y(limit)} className="chart-limit" />}
      </svg>
      <Text size="1" color="gray">
        Highest {format(highest)} · usual {format(Math.round(values.reduce((sum, value) => sum + value.avg, 0) / values.length))}
        {request !== undefined ? ` · reserves ${format(request)}` : ""}
        {limit !== undefined ? ` · ${limitLabel} ${format(limit)}${limitShown ? "" : " (off the chart)"}` : ""}
      </Text>
    </Flex>
  );
}
