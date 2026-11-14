import { Flex, Text } from "@radix-ui/themes";
import { toneOf } from "../lib/sizing";
import { Tone } from "../lib/status";

// What the radar and chips need from a score. Workload sizing and host scores both fit, so
// every score in the app reads the same way.
export interface Scored {
  points: number;
  grade: string;
  tone: Tone;
  // Points lost to shortfalls a person accepted: already out of the score, drawn in gray.
  acceptedPoints?: number;
  sections: { key: string; label: string; asks: string; points: number | null; accepted?: number }[];
}
type Sizing = Scored;

// The sizing score as a shape you can read at a glance: one axis per section (Resources,
// Fit, Scaling, Storage) from the centre outwards, so a lopsided app looks lopsided. The number in the
// middle is the overall score. Colour and the words carry the grade as well, never the
// colour alone.

// One axis per section, spread evenly starting from the top.
const axesFor = (sizing: Sizing) => sizing.sections.map((section, index) => ({ angle: -90 + (360 / sizing.sections.length) * index, key: section.key }));

const point = (cx: number, cy: number, radius: number, angle: number) => {
  const radians = (angle * Math.PI) / 180;
  return [cx + radius * Math.cos(radians), cy + radius * Math.sin(radians)] as const;
};

export function Radar({ sizing, size = 84 }: { sizing: Sizing; size?: number }) {
  const centre = size / 2;
  const radius = size / 2 - 6;
  const AXES = axesFor(sizing);
  const ring = (fraction: number) => AXES.map((axis) => point(centre, centre, radius * fraction, axis.angle).join(",")).join(" ");
  const byKey = Object.fromEntries(sizing.sections.map((section) => [section.key, section]));
  // An axis that does not apply sits on its outer ring, so it neither helps nor hurts the shape.
  // The accepted part sits behind the colored shape in gray, so it reads as "chosen", not "wrong".
  const reach = AXES.map((axis) => point(centre, centre, radius * (Math.min(100, (byKey[axis.key].points ?? 100) + (byKey[axis.key].accepted ?? 0))) / 100, axis.angle).join(",")).join(" ");
  const shape = AXES.map((axis) => point(centre, centre, radius * ((byKey[axis.key].points ?? 100) / 100), axis.angle).join(",")).join(" ");
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-label={`Score ${sizing.points} out of 100, ${sizing.grade}`}>
      <polygon points={ring(1)} className="radar-ring" />
      <polygon points={ring(0.5)} className="radar-ring" />
      {AXES.map((axis) => (
        <line key={axis.key} x1={centre} y1={centre} x2={point(centre, centre, radius, axis.angle)[0]} y2={point(centre, centre, radius, axis.angle)[1]} className="radar-ring" />
      ))}
      {(sizing.acceptedPoints ?? 0) > 0 && <polygon points={reach} className="radar-accepted" />}
      <polygon points={shape} className={`radar-shape radar-${sizing.tone}`} />
      <text x={centre} y={centre} textAnchor="middle" dominantBaseline="central" className="radar-score">{sizing.points}</text>
    </svg>
  );
}

// The four section scores as small labelled numbers.
export function SectionChips({ sizing }: { sizing: Sizing }) {
  return (
    <Flex gap="2" wrap="wrap">
      {sizing.sections.map((section) => (
        <Text key={section.key} size="1" color="gray" title={`${section.label}: ${section.asks}`}>
          {section.label}{" "}
          <Text weight="bold" color={section.points === null ? undefined : toneOf(section.points + (section.accepted ?? 0))}>
            {section.points === null ? "n/a" : section.points}
          </Text>
          {section.accepted ? <Text color="gray" title="Points lost to shortfalls that were accepted"> (−{section.accepted} accepted)</Text> : null}
        </Text>
      ))}
    </Flex>
  );
}
