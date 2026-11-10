import { useEffect, useState } from "react";
import { useBusy } from "../lib/busy";

// The one "please wait" signal: a thin bar across the top of the window while the user
// is waiting on the agent. It appears only after a short delay, so answers that come back
// at once never flash it, and stays a moment once shown so it does not flicker.
const SHOW_AFTER_MS = 250;
const SHOW_AT_LEAST_MS = 500;

export function Throbber() {
  const busy = useBusy();
  const [visible, setVisible] = useState(false);
  const [since, setSince] = useState(0);

  useEffect(() => {
    if (busy) {
      const timer = window.setTimeout(() => {
        setSince(Date.now());
        setVisible(true);
      }, SHOW_AFTER_MS);
      return () => window.clearTimeout(timer);
    }
    if (!visible) return;
    const timer = window.setTimeout(() => setVisible(false), Math.max(0, SHOW_AT_LEAST_MS - (Date.now() - since)));
    return () => window.clearTimeout(timer);
  }, [busy, visible, since]);

  if (!visible) return null;
  return <div className="throbber" role="progressbar" aria-label="Working" />;
}
