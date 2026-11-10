import { useEffect, useState } from "react";
import { authedFetch } from "./auth";

// Usage history recorded by the host agent (internal/usagehistory): per workload and per
// copy, over the last days. Absent when the agent has no history directory, or when this
// agent is not a host agent.

export interface HistoryWorkload {
  namespace: string;
  workload: string;
  hours: number;
  memoryPeak: number;
  memoryP95Peak: number;
  memoryAvg: number;
  cpuPeak: number;
  cpuP95Peak: number;
  cpuAvg: number;
}

export interface HistoryVolume {
  namespace: string;
  volume: string;
  hours: number;
  capacityBytes: number;
  usedBytes: number;
  usedMaxBytes: number;
  // Bytes added per day (a trend, can be negative); 0 until the record spans a day.
  growthPerDay: number;
  spanHours: number;
}

export interface HistoryWindow {
  days: number;
  since?: string;
  hours: number;
}

export interface History {
  window: HistoryWindow;
  // Keyed "namespace/workload".
  byWorkload: Map<string, HistoryWorkload>;
  // Keyed "namespace/volume".
  byVolume: Map<string, HistoryVolume>;
}

export interface HistoryPoint {
  hour: string;
  memoryPeak: number;
  memoryAvg: number;
  cpuPeak: number;
  cpuAvg: number;
}

export interface HistorySeries {
  workload: string;
  points: HistoryPoint[];
}

export interface VolumeSeries {
  volume: string;
  points: { hour: string; usedBytes: number; capacityBytes: number }[];
}

export const HISTORY_DAYS = 7;
// History must span at least this many hours before it is trusted to say a value can be
// made smaller: a shorter record can still catch an app on a quiet day.
export const TRUSTED_HOURS = 48;

async function get<T>(path: string): Promise<T | null> {
  try {
    const response = await authedFetch(path, { quiet: true });
    if (!response.ok) return null;
    const body = await response.json();
    return body?.enabled ? (body as T) : null;
  } catch {
    return null;
  }
}

// useHistory loads the summary now and refreshes it every few minutes.
export function useHistory(): History | null {
  const [history, setHistory] = useState<History | null>(null);
  useEffect(() => {
    let current = true;
    const load = async () => {
      const body = await get<{ summary: { window: HistoryWindow; workloads: HistoryWorkload[]; volumes?: HistoryVolume[] } }>(`/api/v1/usage-history?days=${HISTORY_DAYS}`);
      if (!current) return;
      setHistory(
        body && (body.summary.workloads.length > 0 || (body.summary.volumes ?? []).length > 0)
          ? {
              window: body.summary.window,
              byWorkload: new Map(body.summary.workloads.map((item) => [`${item.namespace}/${item.workload}`, item])),
              byVolume: new Map((body.summary.volumes ?? []).map((item) => [`${item.namespace}/${item.volume}`, item])),
            }
          : null,
      );
    };
    void load();
    const timer = setInterval(load, 5 * 60 * 1000);
    return () => {
      current = false;
      clearInterval(timer);
    };
  }, []);
  return history;
}

// useSeries loads the hourly figures of one app (namespace) for the detail charts: its
// workloads' CPU and memory, and how full its volumes have been.
export function useSeries(namespace: string): { workloads: HistorySeries[]; volumes: VolumeSeries[] } | null {
  const [series, setSeries] = useState<{ workloads: HistorySeries[]; volumes: VolumeSeries[] } | null>(null);
  useEffect(() => {
    let current = true;
    setSeries(null);
    void get<{ series: HistorySeries[]; volumes?: VolumeSeries[] }>(`/api/v1/usage-history/series?${new URLSearchParams({ namespace, days: String(HISTORY_DAYS) })}`).then(
      (body) => current && setSeries(body ? { workloads: body.series ?? [], volumes: body.volumes ?? [] } : null),
    );
    return () => {
      current = false;
    };
  }, [namespace]);
  return series;
}
