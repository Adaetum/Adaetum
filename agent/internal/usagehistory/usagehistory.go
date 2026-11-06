// Package usagehistory remembers how much CPU and memory each workload used over the
// last weeks, so sizing can be judged on what an app does over days rather than on one
// moment (a media server looks idle until it transcodes).
//
// WHAT IS KEPT. Not every sample: one row per workload per hour, holding the busiest
// copy's peak and a running average. A cluster of 200 workloads is then about 5,000 rows
// a day and about 70,000 for the two weeks kept, small enough to live in the agent's
// own database and be queried on a page load.
//
// WHAT IT IS NOT. It is observation only. Nothing here changes the cluster, and it is
// not a metrics system: Prometheus owns that. It exists so the agent can answer "what
// is a safe size for this app?" without depending on one.
//
// Samples are per workload and per copy, because sizing is a per-copy decision: scaling
// out changes how many copies run, not how big one must be.
package usagehistory

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/store"
)

const (
	// Retention is how long hourly rows are kept.
	Retention = 14 * 24 * time.Hour
	// MaxDays caps a query window.
	MaxDays = 14
)

// Migrations create the history table. It is its own database file, not part of the
// host's identity store, because it is observation data that can be thrown away.
var Migrations = []string{
	`CREATE TABLE usage_hourly (
		hour_ms    INTEGER NOT NULL,
		namespace  TEXT NOT NULL,
		workload   TEXT NOT NULL,
		samples    INTEGER NOT NULL,
		copies_max INTEGER NOT NULL,
		mem_peak   INTEGER NOT NULL,
		mem_sum    INTEGER NOT NULL,
		cpu_peak   INTEGER NOT NULL,
		cpu_sum    INTEGER NOT NULL,
		PRIMARY KEY (hour_ms, namespace, workload)
	)`,
	// Volumes: how full each one is, so growth (and so "when will it fill?") can be seen.
	`CREATE TABLE volume_hourly (
		hour_ms        INTEGER NOT NULL,
		namespace      TEXT NOT NULL,
		volume         TEXT NOT NULL,
		capacity_bytes INTEGER NOT NULL,
		used_last      INTEGER NOT NULL,
		used_max       INTEGER NOT NULL,
		PRIMARY KEY (hour_ms, namespace, volume)
	)`,
}

// Sample is one workload at one moment: per copy, the busiest copy and the average.
type Sample struct {
	Namespace  string
	Workload   string
	Copies     int
	MemoryPeak int64 // bytes, busiest copy
	MemoryAvg  int64 // bytes, averaged over copies
	CPUPeak    int64 // millicores, busiest copy
	CPUAvg     int64
}

// VolumeSample is one volume at one moment.
type VolumeSample struct {
	Namespace     string
	Name          string
	CapacityBytes int64
	UsedBytes     int64
}

// Sampled is everything one reading produces.
type Sampled struct {
	Workloads []Sample
	Volumes   []VolumeSample
}

// Store keeps the hourly rows.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

// Open opens (creating) the history database at path.
func Open(path string) (*Store, error) {
	db, err := store.OpenDatabase(path, Migrations)
	if err != nil {
		return nil, err
	}
	return &Store{DB: db, Now: time.Now}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func hourOf(t time.Time) int64 { return t.UTC().Truncate(time.Hour).UnixMilli() }

// Record folds samples taken at `at` into their hour.
func (s *Store) Record(ctx context.Context, at time.Time, samples []Sample) error {
	hour := hourOf(at)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sample := range samples {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO usage_hourly(hour_ms, namespace, workload, samples, copies_max, mem_peak, mem_sum, cpu_peak, cpu_sum)
			 VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?)
			 ON CONFLICT(hour_ms, namespace, workload) DO UPDATE SET
			   samples    = samples + 1,
			   copies_max = max(copies_max, excluded.copies_max),
			   mem_peak   = max(mem_peak, excluded.mem_peak),
			   mem_sum    = mem_sum + excluded.mem_sum,
			   cpu_peak   = max(cpu_peak, excluded.cpu_peak),
			   cpu_sum    = cpu_sum + excluded.cpu_sum`,
			hour, sample.Namespace, sample.Workload, sample.Copies, sample.MemoryPeak, sample.MemoryAvg, sample.CPUPeak, sample.CPUAvg)
		if err != nil {
			return fmt.Errorf("recording %s/%s: %w", sample.Namespace, sample.Workload, err)
		}
	}
	return tx.Commit()
}

// RecordVolumes folds volume readings into their hour: the latest figure and the highest.
func (s *Store) RecordVolumes(ctx context.Context, at time.Time, samples []VolumeSample) error {
	hour := hourOf(at)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sample := range samples {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO volume_hourly(hour_ms, namespace, volume, capacity_bytes, used_last, used_max) VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(hour_ms, namespace, volume) DO UPDATE SET
			   capacity_bytes = excluded.capacity_bytes,
			   used_last      = excluded.used_last,
			   used_max       = max(used_max, excluded.used_max)`,
			hour, sample.Namespace, sample.Name, sample.CapacityBytes, sample.UsedBytes, sample.UsedBytes)
		if err != nil {
			return fmt.Errorf("recording volume %s/%s: %w", sample.Namespace, sample.Name, err)
		}
	}
	return tx.Commit()
}

// Prune drops rows older than the retention.
func (s *Store) Prune(ctx context.Context) error {
	cutoff := s.now().Add(-Retention).UnixMilli()
	for _, table := range []string{"usage_hourly", "volume_hourly"} {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM `+table+` WHERE hour_ms < ?`, cutoff); err != nil {
			return err
		}
	}
	return nil
}

// Window is how much of the asked-for period actually has data.
type Window struct {
	Days  int       `json:"days"`
	Since time.Time `json:"since,omitempty"` // first hour with data
	Hours int       `json:"hours"`           // hours with data (can be fewer than days*24)
}

// WorkloadSummary is what one workload did across the window, per copy.
type WorkloadSummary struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Hours     int    `json:"hours"`
	// Peak is the highest any copy reached; P95Peak is the 95th percentile of the
	// hourly peaks, which ignores a single freak spike. Avg is the mean over time.
	MemoryPeak    int64 `json:"memoryPeak"`
	MemoryP95Peak int64 `json:"memoryP95Peak"`
	MemoryAvg     int64 `json:"memoryAvg"`
	CPUPeak       int64 `json:"cpuPeak"`
	CPUP95Peak    int64 `json:"cpuP95Peak"`
	CPUAvg        int64 `json:"cpuAvg"`
}

// VolumeSummary is how a volume has filled over the window.
type VolumeSummary struct {
	Namespace     string `json:"namespace"`
	Volume        string `json:"volume"`
	Hours         int    `json:"hours"`
	CapacityBytes int64  `json:"capacityBytes"` // latest
	UsedBytes     int64  `json:"usedBytes"`     // latest
	UsedMaxBytes  int64  `json:"usedMaxBytes"`
	// GrowthPerDay is the trend of bytes used per day (least squares over the hourly
	// readings); it can be negative. It is 0 until the record spans a day.
	GrowthPerDay int64 `json:"growthPerDay"`
	SpanHours    int   `json:"spanHours"`
}

type Summary struct {
	Window    Window            `json:"window"`
	Workloads []WorkloadSummary `json:"workloads"`
	Volumes   []VolumeSummary   `json:"volumes"`
}

func clampDays(days int) int {
	if days < 1 {
		return 1
	}
	if days > MaxDays {
		return MaxDays
	}
	return days
}

type row struct {
	hour                     int64
	namespace, workload      string
	samples, memPeak, memSum int64
	cpuPeak, cpuSum          int64
}

func (s *Store) rows(ctx context.Context, since time.Time, namespace string) ([]row, error) {
	query := `SELECT hour_ms, namespace, workload, samples, mem_peak, mem_sum, cpu_peak, cpu_sum FROM usage_hourly WHERE hour_ms >= ?`
	args := []any{hourOf(since)}
	if namespace != "" {
		query += ` AND namespace = ?`
		args = append(args, namespace)
	}
	query += ` ORDER BY namespace, workload, hour_ms`
	result, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	var rows []row
	for result.Next() {
		var r row
		if err := result.Scan(&r.hour, &r.namespace, &r.workload, &r.samples, &r.memPeak, &r.memSum, &r.cpuPeak, &r.cpuSum); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, result.Err()
}

// Summary describes every workload over the last `days` days.
func (s *Store) Summary(ctx context.Context, days int) (Summary, error) {
	days = clampDays(days)
	rows, err := s.rows(ctx, s.now().Add(-time.Duration(days)*24*time.Hour), "")
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{Window: Window{Days: days}, Workloads: []WorkloadSummary{}, Volumes: []VolumeSummary{}}
	hours := map[int64]bool{}
	for start := 0; start < len(rows); {
		end := start
		for end < len(rows) && rows[end].namespace == rows[start].namespace && rows[end].workload == rows[start].workload {
			end++
		}
		group := rows[start:end]
		start = end

		var samples, memSum, cpuSum int64
		memPeaks, cpuPeaks := make([]int64, 0, len(group)), make([]int64, 0, len(group))
		for _, r := range group {
			samples += r.samples
			memSum += r.memSum
			cpuSum += r.cpuSum
			memPeaks = append(memPeaks, r.memPeak)
			cpuPeaks = append(cpuPeaks, r.cpuPeak)
			hours[r.hour] = true
		}
		summary.Workloads = append(summary.Workloads, WorkloadSummary{
			Namespace: group[0].namespace, Workload: group[0].workload, Hours: len(group),
			MemoryPeak: maxOf(memPeaks), MemoryP95Peak: percentile95(memPeaks), MemoryAvg: memSum / samples,
			CPUPeak: maxOf(cpuPeaks), CPUP95Peak: percentile95(cpuPeaks), CPUAvg: cpuSum / samples,
		})
	}
	volumes, err := s.volumeRows(ctx, s.now().Add(-time.Duration(days)*24*time.Hour), "")
	if err != nil {
		return Summary{}, err
	}
	for _, group := range groupVolumes(volumes) {
		summary.Volumes = append(summary.Volumes, summarizeVolume(group))
		for _, r := range group {
			hours[r.hour] = true
		}
	}
	summary.Window.Hours = len(hours)
	for hour := range hours {
		if t := time.UnixMilli(hour).UTC(); summary.Window.Since.IsZero() || t.Before(summary.Window.Since) {
			summary.Window.Since = t
		}
	}
	return summary, nil
}

type volumeRow struct {
	hour                 int64
	namespace, volume    string
	capacity, last, most int64
}

func (s *Store) volumeRows(ctx context.Context, since time.Time, namespace string) ([]volumeRow, error) {
	query := `SELECT hour_ms, namespace, volume, capacity_bytes, used_last, used_max FROM volume_hourly WHERE hour_ms >= ?`
	args := []any{hourOf(since)}
	if namespace != "" {
		query += ` AND namespace = ?`
		args = append(args, namespace)
	}
	query += ` ORDER BY namespace, volume, hour_ms`
	result, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	var rows []volumeRow
	for result.Next() {
		var r volumeRow
		if err := result.Scan(&r.hour, &r.namespace, &r.volume, &r.capacity, &r.last, &r.most); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, result.Err()
}

func groupVolumes(rows []volumeRow) [][]volumeRow {
	var groups [][]volumeRow
	for start := 0; start < len(rows); {
		end := start
		for end < len(rows) && rows[end].namespace == rows[start].namespace && rows[end].volume == rows[start].volume {
			end++
		}
		groups = append(groups, rows[start:end])
		start = end
	}
	return groups
}

func summarizeVolume(group []volumeRow) VolumeSummary {
	latest := group[len(group)-1]
	summary := VolumeSummary{
		Namespace: latest.namespace, Volume: latest.volume, Hours: len(group),
		CapacityBytes: latest.capacity, UsedBytes: latest.last,
	}
	for _, r := range group {
		if r.most > summary.UsedMaxBytes {
			summary.UsedMaxBytes = r.most
		}
	}
	summary.SpanHours = int((latest.hour - group[0].hour) / int64(time.Hour/time.Millisecond))
	if summary.SpanHours >= 24 {
		summary.GrowthPerDay = slopePerDay(group)
	}
	return summary
}

// slopePerDay is the least-squares trend of the hourly "used" readings, in bytes per day.
func slopePerDay(group []volumeRow) int64 {
	n := float64(len(group))
	var sumX, sumY, sumXY, sumXX float64
	origin := float64(group[0].hour)
	for _, r := range group {
		x := (float64(r.hour) - origin) / float64(24*time.Hour/time.Millisecond) // days
		y := float64(r.last)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return 0
	}
	return int64(math.Round((n*sumXY - sumX*sumY) / denominator))
}

// VolumePoint is one hour of one volume.
type VolumePoint struct {
	Hour          time.Time `json:"hour"`
	UsedBytes     int64     `json:"usedBytes"`
	CapacityBytes int64     `json:"capacityBytes"`
}

type VolumeSeries struct {
	Volume string        `json:"volume"`
	Points []VolumePoint `json:"points"`
}

// VolumeSeries returns the hourly fullness of every volume in a namespace, for charts.
func (s *Store) VolumeSeries(ctx context.Context, namespace string, days int) ([]VolumeSeries, error) {
	rows, err := s.volumeRows(ctx, s.now().Add(-time.Duration(clampDays(days))*24*time.Hour), namespace)
	if err != nil {
		return nil, err
	}
	all := []VolumeSeries{}
	for _, group := range groupVolumes(rows) {
		series := VolumeSeries{Volume: group[0].volume, Points: make([]VolumePoint, 0, len(group))}
		for _, r := range group {
			series.Points = append(series.Points, VolumePoint{Hour: time.UnixMilli(r.hour).UTC(), UsedBytes: r.last, CapacityBytes: r.capacity})
		}
		all = append(all, series)
	}
	return all, nil
}

// Point is one hour of one workload.
type Point struct {
	Hour       time.Time `json:"hour"`
	MemoryPeak int64     `json:"memoryPeak"`
	MemoryAvg  int64     `json:"memoryAvg"`
	CPUPeak    int64     `json:"cpuPeak"`
	CPUAvg     int64     `json:"cpuAvg"`
}

type Series struct {
	Workload string  `json:"workload"`
	Points   []Point `json:"points"`
}

// Series returns the hourly figures of every workload in a namespace, for charts.
func (s *Store) Series(ctx context.Context, namespace string, days int) ([]Series, error) {
	days = clampDays(days)
	rows, err := s.rows(ctx, s.now().Add(-time.Duration(days)*24*time.Hour), namespace)
	if err != nil {
		return nil, err
	}
	all := []Series{}
	for _, r := range rows {
		if len(all) == 0 || all[len(all)-1].Workload != r.workload {
			all = append(all, Series{Workload: r.workload, Points: []Point{}})
		}
		last := &all[len(all)-1]
		last.Points = append(last.Points, Point{
			Hour: time.UnixMilli(r.hour).UTC(), MemoryPeak: r.memPeak, MemoryAvg: r.memSum / r.samples,
			CPUPeak: r.cpuPeak, CPUAvg: r.cpuSum / r.samples,
		})
	}
	return all, nil
}

func maxOf(values []int64) int64 {
	var best int64
	for _, v := range values {
		if v > best {
			best = v
		}
	}
	return best
}

func percentile95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

// Run samples on a timer until ctx ends. A failed sample is reported through logf and
// skipped: history is a convenience and must never stop or slow the agent.
func Run(ctx context.Context, store *Store, every time.Duration, sample func(context.Context) (Sampled, error), logf func(string, ...any)) {
	take := func() {
		sampleCtx, cancel := context.WithTimeout(ctx, every)
		defer cancel()
		sampled, err := sample(sampleCtx)
		if err != nil && ctx.Err() == nil {
			logf("usage history: cannot sample: %v", err)
		}
		// Whatever was read is kept, even if the other half failed.
		if err := store.Record(ctx, store.now(), sampled.Workloads); err != nil && ctx.Err() == nil {
			logf("usage history: cannot record: %v", err)
		}
		if err := store.RecordVolumes(ctx, store.now(), sampled.Volumes); err != nil && ctx.Err() == nil {
			logf("usage history: cannot record volumes: %v", err)
		}
	}
	take()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			take()
		case <-prune.C:
			if err := store.Prune(ctx); err != nil && ctx.Err() == nil {
				logf("usage history: cannot prune: %v", err)
			}
		}
	}
}
