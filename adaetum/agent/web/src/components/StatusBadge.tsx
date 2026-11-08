import { Badge } from "@radix-ui/themes";
import { ReactNode } from "react";
import { Tone } from "../lib/status";

interface StatusBadgeProps {
  tone: Tone;
  children: ReactNode;
  // The raw technical value, shown on hover for operators who want it.
  title?: string;
}

// One badge style everywhere so a color always means the same thing. Status is
// also carried by the label text, never by color alone. highContrast is needed
// because the default soft green and amber fall just under WCAG AA (4.5:1) for
// small text in the light theme.
export function StatusBadge({ tone, children, title }: StatusBadgeProps) {
  return (
    <Badge color={tone} variant="soft" radius="full" highContrast title={title}>
      {children}
    </Badge>
  );
}
