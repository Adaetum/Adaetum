import { ReactNode } from "react";

interface ScrollRegionProps {
  // Names the region for screen readers ("Cleanup history table").
  label: string;
  children: ReactNode;
  // Let cells wrap instead of forcing one line, so the region scrolls less often.
  wrap?: boolean;
}

// ScrollRegion wraps a table that may be wider than its container. A region that
// scrolls must be reachable by keyboard, so it is focusable and named. Radix
// tables carry their own inner scroll area; the CSS for .table-scroll turns that
// one off, so there is exactly one scrolling element, and it is this one.
export function ScrollRegion({ label, children, wrap }: ScrollRegionProps) {
  return (
    <div className={wrap ? "table-scroll wrap" : "table-scroll"} role="region" aria-label={label} tabIndex={0}>
      {children}
    </div>
  );
}
