package backups

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// VolumeAPI serves POST /api/v1/backups/volumes/{name}: choose whether a volume is backed up.
// The change is a label on the Longhorn volume, so it takes effect at once and needs no pull
// request; it is recorded first, and only the one backup label is ever touched.
type VolumeAPI struct {
	// SetLabels sets (non-nil) or removes (nil) labels on a Longhorn volume.
	SetLabels   func(ctx context.Context, name string, labels map[string]*string) error
	Audit       func(entry any) error
	RequireUser func(http.Handler) http.Handler
	Who         func(*http.Request) string
	// Get reads the cluster, to find a volume's claim and whether VolSync already copies others.
	Get Getter
	// Sync proposes putting a volume on or off sync (a pull request). Nil means sync is unavailable.
	Sync *SyncChanger
}

// Longhorn names a volume after its PersistentVolume, such as pvc-<uuid>.
var volumeName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// Register adds the route behind the sign-in guard.
func (a *VolumeAPI) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(a.serve)
	if a.RequireUser != nil {
		handler = a.RequireUser(handler)
	}
	mux.Handle("POST /api/v1/backups/volumes/{name}", handler)
}

func (a *VolumeAPI) serve(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		// Mode is "off", "sync", or "schedule:<name>" (on that named schedule).
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body); err != nil || !volumeName.MatchString(name) || !validMode(body.Mode) {
		reply(w, http.StatusBadRequest, map[string]any{"error": `Send {"mode": "off"}, {"mode": "sync"} or {"mode": "schedule:<name>"} for a volume.`})
		return
	}
	if a.Audit == nil || a.SetLabels == nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because there is no record to write them to."})
		return
	}
	who := ""
	if a.Who != nil {
		who = a.Who(r)
	}
	if err := a.Audit(map[string]any{"time": time.Now().UTC(), "action": "volume-backup", "volume": name, "mode": body.Mode, "by": who}); err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because the record cannot be written."})
		return
	}
	// Sync is a pull request. It does not take the volume off the schedule here: the daily backup
	// keeps protecting it until the pull request is merged and the copy exists (EnforceOneMode
	// then removes the schedule). The volume is only marked chosen, so the default rule leaves it be.
	proposal, err := a.change(r.Context(), name, body.Mode)
	if err != nil {
		reply(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	reply(w, http.StatusOK, map[string]string{"volume": name, "mode": body.Mode, "proposalUrl": proposal})
}

// validMode accepts "off", "sync" and "schedule:<name>".
func validMode(mode string) bool {
	if mode == "off" || mode == "sync" {
		return true
	}
	name, ok := strings.CutPrefix(mode, "schedule:")
	return ok && scheduleName.MatchString(name)
}

// change applies a mode and returns the pull request's address when one was needed.
func (a *VolumeAPI) change(ctx context.Context, name, mode string) (string, error) {
	if a.Get == nil {
		return "", errors.New("the cluster cannot be read")
	}
	state, err := Collect(ctx, a.Get, time.Now())
	if err != nil {
		return "", err
	}
	var volume Volume
	for _, candidate := range state.Volumes {
		if candidate.Name == name {
			volume = candidate
		}
	}
	if volume.Name == "" {
		return "", errors.New("there is no such volume")
	}
	if schedule, ok := strings.CutPrefix(mode, "schedule:"); ok && !hasSchedule(state, schedule) {
		return "", errors.New("there is no active schedule called " + schedule + ". If it was just added, merge its pull request first.")
	}
	proposal := ""
	if mode == "sync" || volume.Sync {
		if a.Sync == nil {
			return "", errors.New("sync is not available on this agent")
		}
		others := 0
		for _, candidate := range state.Volumes {
			if candidate.Sync && candidate.Name != name {
				others++
			}
		}
		proposal, err = a.Sync.Change(ctx, SyncChange{Namespace: volume.Namespace, Claim: volume.Claim, Enable: mode == "sync", FirstUse: mode == "sync" && !state.SyncInstalled && others == 0, LastUse: mode != "sync" && others == 0})
		if err != nil {
			return "", err
		}
	}
	// Sync is a pull request. It does not take the volume off its schedule here: the schedule
	// keeps protecting it until the pull request is merged and the copy exists (EnforceOneMode
	// then removes it). The volume is only marked chosen, so the default rule leaves it be.
	labels := modeLabels(mode, scheduleNames(state), true)
	if mode == "sync" {
		for key := range labels {
			if strings.HasPrefix(key, groupLabel) {
				delete(labels, key)
			}
		}
	}
	return proposal, a.SetLabels(ctx, name, labels)
}

func hasSchedule(state State, name string) bool {
	for _, schedule := range state.Schedules {
		if schedule.Name == name {
			return true
		}
	}
	return false
}

func scheduleNames(state State) []string {
	names := make([]string, 0, len(state.Schedules))
	for _, schedule := range state.Schedules {
		names = append(names, schedule.Name)
	}
	return names
}

// ChosenLabel marks a volume whose mode somebody decided (or the default rule applied). The
// default rule leaves marked volumes alone, so choosing "not backed up" for a volume sticks.
const ChosenLabel = "adaetum.io/backup-chosen"

// modeLabels is the label change for a mode. Every known schedule's label is cleared and the
// chosen one set, so a volume is on at most one schedule. chosen marks the volume as decided.
func modeLabels(mode string, known []string, chosen bool) map[string]*string {
	enabled, yes := "enabled", "true"
	labels := map[string]*string{}
	for _, name := range known {
		labels[groupLabel+GroupPrefix+name] = nil
	}
	if name, ok := strings.CutPrefix(mode, "schedule:"); ok {
		labels[groupLabel+GroupPrefix+name] = &enabled
	}
	if chosen {
		labels[ChosenLabel] = &yes
	}
	return labels
}

// EnforceOneMode takes a volume off its schedule once a sync copy of it exists, so a volume is
// in one mode at a time. It runs with the default rule's loop and records each change.
func EnforceOneMode(ctx context.Context, get Getter, set func(context.Context, string, map[string]*string) error, audit func(any) error) error {
	state, err := Collect(ctx, get, time.Now())
	if err != nil {
		return err
	}
	for _, volume := range state.Volumes {
		if volume.Mode != "both" || volume.LastSyncAt == nil {
			continue
		}
		if audit(map[string]any{"time": time.Now().UTC(), "action": "volume-backup", "volume": volume.Name, "mode": "sync", "by": "one mode at a time"}) != nil {
			continue
		}
		clear := map[string]*string{}
		for _, name := range scheduleNames(state) {
			clear[groupLabel+GroupPrefix+name] = nil
		}
		if err := set(ctx, volume.Name, clear); err != nil {
			return err
		}
	}
	return nil
}
