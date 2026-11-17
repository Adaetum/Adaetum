package backups

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// The default rule: what happens to a volume created after the rule was set. A new volume is
// the failure this exists for: an operator adds an app, its data volume appears, and nobody
// remembers to choose a mode for it. The rule applies only to volumes created AFTER it was
// set, never to existing ones, so turning it on cannot quietly change what is already decided.
// A volume somebody chose a mode for (ChosenLabel) is never touched by it.

// Default is the saved rule.
type Default struct {
	// Mode is "off" (new volumes are left alone) or "schedule:<name>".
	Mode string `json:"mode"`
	// Since is when the rule was set; only volumes created after it are affected.
	Since time.Time `json:"since"`
}

// DefaultStore keeps the rule in a small file on the agent's volume.
type DefaultStore struct {
	Path string
	mu   sync.Mutex
}

// Get returns the rule; with no file yet it is "off".
func (d *DefaultStore) Get() Default {
	d.mu.Lock()
	defer d.mu.Unlock()
	var rule Default
	if data, err := os.ReadFile(d.Path); err == nil {
		_ = json.Unmarshal(data, &rule)
	}
	if !validMode(rule.Mode) || rule.Mode == "sync" {
		rule.Mode = "off"
	}
	return rule
}

// Set saves the rule from now on.
func (d *DefaultStore) Set(mode string, now time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	data, _ := json.Marshal(Default{Mode: mode, Since: now.UTC()})
	return os.WriteFile(d.Path, data, 0o600)
}

// Reconcile labels every volume created after the rule was set that nobody has chosen a mode
// for. It returns the volumes it changed. Each change is recorded first; a failed record skips
// that volume rather than changing it unrecorded.
func Reconcile(ctx context.Context, get Getter, set func(context.Context, string, map[string]*string) error, audit func(any) error, rule Default) ([]string, error) {
	if rule.Mode == "off" {
		return nil, nil
	}
	state, err := Collect(ctx, get, time.Now())
	if err != nil {
		return nil, err
	}
	schedule, _ := strings.CutPrefix(rule.Mode, "schedule:")
	if !hasSchedule(state, schedule) {
		// The schedule was removed or is not merged yet: do nothing rather than guess another.
		return nil, nil
	}
	data, err := get(ctx, "volumes.longhorn.io", "-n", "longhorn-system")
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, item := range items(data) {
		if item.Metadata.Labels[ChosenLabel] != "" || !item.Metadata.CreationTimestamp.After(rule.Since) {
			continue
		}
		if audit(map[string]any{"time": time.Now().UTC(), "action": "volume-backup", "volume": item.Metadata.Name, "mode": rule.Mode, "by": "default rule"}) != nil {
			continue
		}
		if err := set(ctx, item.Metadata.Name, modeLabels(rule.Mode, scheduleNames(state), true)); err != nil {
			return changed, err
		}
		changed = append(changed, item.Metadata.Name)
	}
	return changed, nil
}

// DefaultAPI serves GET and POST /api/v1/backups/default.
type DefaultAPI struct {
	Store       *DefaultStore
	Audit       func(entry any) error
	RequireUser func(http.Handler) http.Handler
	Who         func(*http.Request) string
}

// Register adds the routes behind the sign-in guard.
func (a *DefaultAPI) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(a.serve)
	if a.RequireUser != nil {
		handler = a.RequireUser(handler)
	}
	mux.Handle("/api/v1/backups/default", handler)
}

func (a *DefaultAPI) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		reply(w, http.StatusOK, a.Store.Get())
	case http.MethodPost:
		var body struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || (body.Mode != "off" && !strings.HasPrefix(body.Mode, "schedule:") || !validMode(body.Mode)) {
			reply(w, http.StatusBadRequest, map[string]any{"error": `Send {"mode": "off"} or {"mode": "schedule:<name>"}.`})
			return
		}
		if a.Audit == nil {
			reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because there is no record to write them to."})
			return
		}
		who := ""
		if a.Who != nil {
			who = a.Who(r)
		}
		if err := a.Audit(map[string]any{"time": time.Now().UTC(), "action": "default-rule", "mode": body.Mode, "by": who}); err != nil {
			reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because the record cannot be written."})
			return
		}
		if err := a.Store.Set(body.Mode, time.Now()); err != nil {
			reply(w, http.StatusInternalServerError, map[string]any{"error": errors.New("the rule could not be saved").Error()})
			return
		}
		reply(w, http.StatusOK, a.Store.Get())
	default:
		w.Header().Set("Allow", "GET, POST")
		reply(w, http.StatusMethodNotAllowed, map[string]any{"error": "use GET or POST"})
	}
}
