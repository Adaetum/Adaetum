const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

export function formatBytes(bytes: number): string {
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${UNITS[unit]}`;
}

export function formatDuration(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m`;
  return `${Math.floor(seconds)}s`;
}

// formatAgo renders how long before `now` a timestamp was, e.g. "5m ago".
export function formatAgo(iso: string, now: Date): string {
  const seconds = (now.getTime() - new Date(iso).getTime()) / 1000;
  if (Number.isNaN(seconds)) return "—";
  return `${formatDuration(Math.max(0, seconds))} ago`;
}

export function plural(count: number, singular: string, pluralForm = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : pluralForm}`;
}

// shorten trims very long identifiers (container scopes are 70+ characters).
// Full values stay available in the detailed views.
export function shorten(value: string, max = 36): string {
  return value.length > max ? `${value.slice(0, max)}…` : value;
}

// formatDaysRemaining renders certificate lifetimes: "54 days", "9.7 years".
export function formatDaysRemaining(days: number): string {
  if (days < 0) return `expired ${plural(-days, "day")} ago`;
  if (days >= 365) return `${(days / 365).toFixed(1)} years`;
  return plural(days, "day");
}
