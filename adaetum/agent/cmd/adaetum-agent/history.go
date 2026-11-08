package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
	"github.com/Adaetum/Adaetum/agent/internal/usagehistory"
)

const (
	historyDatabaseName = "usage-history.db"
	// historyEvery is how often usage is sampled. Hourly rows keep the peak and the
	// average of these samples, so a shorter interval catches shorter spikes.
	historyEvery = 5 * time.Minute
)

// startUsageHistory records how much each workload uses over time and serves it, so
// sizing can be judged over days instead of one moment. It is off without a directory.
//
// It lives in the agent that can run kubectl (the host agent), observes only, and never
// stops or slows the agent: a database that will not open is reported and the routes
// then say history is off. Unlike -data-dir it is allowed with -ssh: it holds cluster
// usage, not this machine's identity, and is a dev aid there.
func startUsageHistory(mux *http.ServeMux, dir string, inspector *inspect.Inspector) func() {
	disabled := func(w http.ResponseWriter) { writeJSONValue(w, map[string]any{"enabled": false}) }
	if dir == "" {
		mux.HandleFunc("GET /api/v1/usage-history", func(w http.ResponseWriter, _ *http.Request) { disabled(w) })
		mux.HandleFunc("GET /api/v1/usage-history/series", func(w http.ResponseWriter, _ *http.Request) { disabled(w) })
		return func() {}
	}
	path := filepath.Join(dir, historyDatabaseName)
	history, err := usagehistory.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage history: cannot open the database; continuing without it:", err)
		mux.HandleFunc("GET /api/v1/usage-history", func(w http.ResponseWriter, _ *http.Request) { disabled(w) })
		mux.HandleFunc("GET /api/v1/usage-history/series", func(w http.ResponseWriter, _ *http.Request) { disabled(w) })
		return func() {}
	}
	fmt.Fprintf(os.Stderr, "usage history: recording every %s into %s (kept %d days)\n", historyEvery, path, int(usagehistory.Retention.Hours()/24))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		usagehistory.Run(ctx, history, historyEvery, func(ctx context.Context) (usagehistory.Sampled, error) {
			var sampled usagehistory.Sampled
			// Workloads and volumes are read independently: one failing must not lose the other.
			usage, usageErr := inspector.WorkloadUsage(ctx)
			for _, u := range usage {
				sampled.Workloads = append(sampled.Workloads, usagehistory.Sample{
					Namespace: u.Namespace, Workload: u.Name, Copies: u.Copies,
					MemoryPeak: u.MemoryPeak, MemoryAvg: u.MemoryAvg, CPUPeak: u.CPUPeak, CPUAvg: u.CPUAvg,
				})
			}
			volumes, volumeErr := inspector.VolumeUsage(ctx)
			for _, v := range volumes {
				sampled.Volumes = append(sampled.Volumes, usagehistory.VolumeSample{Namespace: v.Namespace, Name: v.Name, CapacityBytes: v.CapacityBytes, UsedBytes: v.UsedBytes})
			}
			return sampled, errors.Join(usageErr, volumeErr)
		}, func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) })
	}()

	days := func(r *http.Request) int {
		value, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if value == 0 {
			value = 7
		}
		return value
	}
	mux.HandleFunc("GET /api/v1/usage-history", func(w http.ResponseWriter, r *http.Request) {
		summary, err := history.Summary(r.Context(), days(r))
		if err != nil {
			http.Error(w, "usage history could not be read", http.StatusInternalServerError)
			return
		}
		writeJSONValue(w, map[string]any{"enabled": true, "summary": summary})
	})
	mux.HandleFunc("GET /api/v1/usage-history/series", func(w http.ResponseWriter, r *http.Request) {
		namespace := r.URL.Query().Get("namespace")
		if namespace == "" || len(namespace) > 63 {
			http.Error(w, "namespace is required", http.StatusBadRequest)
			return
		}
		series, err := history.Series(r.Context(), namespace, days(r))
		if err != nil {
			http.Error(w, "usage history could not be read", http.StatusInternalServerError)
			return
		}
		volumes, err := history.VolumeSeries(r.Context(), namespace, days(r))
		if err != nil {
			http.Error(w, "usage history could not be read", http.StatusInternalServerError)
			return
		}
		writeJSONValue(w, map[string]any{"enabled": true, "series": series, "volumes": volumes})
	})
	return func() {
		cancel()
		<-done
		_ = history.Close()
	}
}

func writeJSONValue(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
