package usagehistory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T, now time.Time) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.Now = func() time.Time { return now }
	return s
}

func TestSamplesInOneHourFoldIntoOnePeakAndAverage(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 30, 0, 0, time.UTC)
	s := open(t, now)
	ctx := context.Background()
	for _, sample := range []Sample{
		{Namespace: "media", Workload: "plex", Copies: 1, MemoryPeak: 100, MemoryAvg: 100, CPUPeak: 10, CPUAvg: 10},
		{Namespace: "media", Workload: "plex", Copies: 2, MemoryPeak: 300, MemoryAvg: 200, CPUPeak: 50, CPUAvg: 30},
	} {
		if err := s.Record(ctx, now, []Sample{sample}); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := s.Summary(ctx, 7)
	if err != nil || len(summary.Workloads) != 1 {
		t.Fatalf("%+v %v", summary, err)
	}
	got := summary.Workloads[0]
	if got.MemoryPeak != 300 || got.MemoryAvg != 150 || got.CPUPeak != 50 || got.CPUAvg != 20 || got.Hours != 1 {
		t.Fatalf("the peak is the highest and the average is over samples: %+v", got)
	}
	if summary.Window.Hours != 1 || !summary.Window.Since.Equal(now.Truncate(time.Hour)) {
		t.Fatalf("%+v", summary.Window)
	}
}

func TestAFreakSpikeDoesNotSetTheP95AndOldDataIsPruned(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s := open(t, now)
	ctx := context.Background()
	// 40 ordinary hours at 100, one freak hour at 9000, and one hour older than retention.
	for hour := 0; hour < 40; hour++ {
		_ = s.Record(ctx, now.Add(-time.Duration(hour+1)*time.Hour), []Sample{{Namespace: "a", Workload: "w", MemoryPeak: 100, MemoryAvg: 90}})
	}
	_ = s.Record(ctx, now.Add(-50*time.Hour), []Sample{{Namespace: "a", Workload: "w", MemoryPeak: 9000, MemoryAvg: 90}})
	_ = s.Record(ctx, now.Add(-20*24*time.Hour), []Sample{{Namespace: "a", Workload: "w", MemoryPeak: 5, MemoryAvg: 5}})

	summary, _ := s.Summary(ctx, 14)
	got := summary.Workloads[0]
	if got.MemoryPeak != 9000 || got.MemoryP95Peak != 100 {
		t.Fatalf("the peak keeps the spike but p95 ignores it: %+v", got)
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = s.DB.QueryRow(`SELECT count(*) FROM usage_hourly`).Scan(&left)
	if left != 41 {
		t.Fatalf("only the 20-day-old row should go, %d rows are left", left)
	}
}

func TestSeriesListsEachWorkloadsHoursInOrderAndWindowsAreClamped(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	s := open(t, now)
	ctx := context.Background()
	for _, workload := range []string{"b", "a"} {
		for hour := 3; hour >= 1; hour-- {
			_ = s.Record(ctx, now.Add(-time.Duration(hour)*time.Hour), []Sample{{Namespace: "n", Workload: workload, MemoryPeak: int64(hour)}})
		}
	}
	_ = s.Record(ctx, now, []Sample{{Namespace: "other", Workload: "x", MemoryPeak: 1}})
	series, err := s.Series(ctx, "n", 1000)
	if err != nil || len(series) != 2 || series[0].Workload != "a" || len(series[0].Points) != 3 {
		t.Fatalf("%+v %v", series, err)
	}
	if !series[0].Points[0].Hour.Before(series[0].Points[1].Hour) {
		t.Fatal("points must be oldest first")
	}
	if clampDays(0) != 1 || clampDays(99) != MaxDays {
		t.Fatal("windows are clamped")
	}
}

func TestVolumesKeepTheLatestFigureAndShowHowFastTheyFill(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s := open(t, now)
	ctx := context.Background()
	const GB = 1 << 30
	// 3 days, one reading an hour, growing 1 GiB per day from 10 GiB, capacity 20 GiB.
	for hour := 72; hour >= 0; hour-- {
		used := int64(10*GB) + int64(float64(72-hour)/24*GB)
		_ = s.RecordVolumes(ctx, now.Add(-time.Duration(hour)*time.Hour), []VolumeSample{{Namespace: "a", Name: "data", CapacityBytes: 20 * GB, UsedBytes: used}})
	}
	// A second reading inside the same hour replaces "latest" but not the highest.
	_ = s.RecordVolumes(ctx, now, []VolumeSample{{Namespace: "a", Name: "data", CapacityBytes: 20 * GB, UsedBytes: 12*GB + 1000}})
	_ = s.RecordVolumes(ctx, now, []VolumeSample{{Namespace: "a", Name: "flat", CapacityBytes: GB, UsedBytes: 5}})

	summary, err := s.Summary(ctx, 7)
	if err != nil || len(summary.Volumes) != 2 {
		t.Fatalf("%+v %v", summary.Volumes, err)
	}
	data := summary.Volumes[0]
	if data.Volume != "data" || data.CapacityBytes != 20*GB || data.UsedBytes != 13*GB && data.UsedBytes != 12*GB+1000 {
		t.Fatalf("%+v", data)
	}
	if growth := float64(data.GrowthPerDay) / GB; growth < 0.95 || growth > 1.05 {
		t.Fatalf("it fills about 1 GiB a day, got %.2f", growth)
	}
	if flat := summary.Volumes[1]; flat.GrowthPerDay != 0 || flat.SpanHours != 0 {
		t.Fatalf("one reading is no trend: %+v", flat)
	}
	series, err := s.VolumeSeries(ctx, "a", 7)
	if err != nil || len(series) != 2 || series[0].Volume != "data" || len(series[0].Points) != 73 {
		t.Fatalf("%d %v", len(series), err)
	}
}
