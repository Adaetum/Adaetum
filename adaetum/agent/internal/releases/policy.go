package releases

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Update modes. Every mode stops at a reviewed GitOps pull request: the agent never
// merges and never changes running workloads or hosts itself.
const (
	ModeManual = "manual" // never checks on its own; the Check button still works
	ModeNotify = "notify" // checks on a schedule and says when an update is available
	ModeAuto   = "auto"   // as notify, and opens the pull request once per new release
)

// checkEvery is how often the scheduler asks GitHub. Releases are rare, so a few
// requests a day is plenty and stays far below GitHub's anonymous rate limit.
const checkEvery = 6 * time.Hour

// Settings is the operator's update preference. It is a runtime choice of this
// agent (kept in its data directory), not cluster configuration, so it is not in
// platform.yaml and no renderer reads it.
type Settings struct {
	Channel string `json:"channel"`
	Mode    string `json:"mode"`
}

// Proposed remembers the last release the agent opened a pull request for, so a
// restart or a later check does not open the same pull request again.
type Proposed struct {
	Version string    `json:"version"`
	URL     string    `json:"url"`
	Branch  string    `json:"branch,omitempty"`
	At      time.Time `json:"at"`
}

// Status is what the page shows beside the settings.
type Status struct {
	Settings
	Current         string    `json:"current"`
	CheckedAt       time.Time `json:"checkedAt,omitempty"`
	UpdateAvailable bool      `json:"updateAvailable"`
	Latest          string    `json:"latest,omitempty"`
	Error           string    `json:"error,omitempty"`
	Proposal        *Proposed `json:"proposal,omitempty"`
}

type stored struct {
	Settings
	Proposal *Proposed `json:"proposal,omitempty"`
}

// Policy owns the update preference and the scheduled check.
type Policy struct {
	Path    string // JSON file in the agent's data directory
	Current string
	Latest  func(context.Context, string, string) (Check, error)
	Propose func(context.Context, Manifest) (Proposal, error) // nil: auto mode cannot open pull requests
	Audit   func(any) error
	Now     func() time.Time

	mu     sync.Mutex
	loaded bool
	data   stored
	status Status
	wake   chan struct{}
}

func (p *Policy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// load reads the file once. A missing or unreadable file means the safe default:
// stable channel, manual mode, so a damaged file can never start automatic work.
func (p *Policy) load() {
	if p.loaded {
		return
	}
	p.loaded = true
	p.data = stored{Settings: Settings{Channel: "stable", Mode: ModeManual}}
	if raw, err := os.ReadFile(p.Path); err == nil {
		var s stored
		if json.Unmarshal(raw, &s) == nil && validSettings(s.Settings) == nil {
			p.data = s
		}
	}
	p.status = Status{Settings: p.data.Settings, Current: p.Current, Proposal: p.data.Proposal}
}

func validSettings(s Settings) error {
	if s.Channel != "stable" && s.Channel != "beta" {
		return errors.New("channel must be stable or beta")
	}
	if s.Mode != ModeManual && s.Mode != ModeNotify && s.Mode != ModeAuto {
		return errors.New("mode must be manual, notify or auto")
	}
	return nil
}

// save writes through a temporary file so a crash cannot leave half a file.
func (p *Policy) save() error {
	raw, err := json.MarshalIndent(p.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o750); err != nil {
		return err
	}
	tmp := p.Path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.Path)
}

// Status returns the current settings and the outcome of the last scheduled check.
func (p *Policy) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.load()
	st := p.status
	st.Settings, st.Proposal = p.data.Settings, p.data.Proposal
	return st
}

// Set stores new settings. Changing the channel clears the last result, which was
// for the other channel, and a change to anything but manual checks again soon.
func (p *Policy) Set(s Settings, by string) error {
	if err := validSettings(s); err != nil {
		return err
	}
	p.mu.Lock()
	p.load()
	old := p.data.Settings
	if old == s {
		p.mu.Unlock()
		return nil
	}
	// Record before acting, as every other change in this agent does.
	if p.Audit != nil {
		if err := p.Audit(map[string]any{"time": p.now().UTC(), "event": "update_settings_changed", "channel": s.Channel, "mode": s.Mode, "previous_channel": old.Channel, "previous_mode": old.Mode, "by": by}); err != nil {
			p.mu.Unlock()
			return errors.New("cannot record the change; nothing was changed")
		}
	}
	p.data.Settings = s
	if old.Channel != s.Channel {
		p.status = Status{Current: p.Current}
	}
	err := p.save()
	p.mu.Unlock()
	if err == nil && s.Mode != ModeManual && p.wake != nil {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
	return err
}

// Check runs one scheduled cycle: look for a release and, in auto mode, open the
// pull request for it unless one was already opened for that version.
func (p *Policy) Check(ctx context.Context) {
	p.mu.Lock()
	p.load()
	settings := p.data.Settings
	p.mu.Unlock()
	if settings.Mode == ModeManual {
		return
	}
	result, err := p.Latest(ctx, p.Current, settings.Channel)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.data.Settings != settings {
		return // the operator changed their mind while GitHub was answering
	}
	p.status.CheckedAt = p.now().UTC()
	p.status.Error = ""
	if err != nil {
		p.status.Error = "Could not check Adaetum releases: " + err.Error()
		return
	}
	p.status.UpdateAvailable, p.status.Latest = result.UpdateAvailable, result.Release.Version
	if !result.UpdateAvailable || settings.Mode != ModeAuto {
		return
	}
	if prior := p.data.Proposal; prior != nil && prior.Version == result.Release.Version {
		return
	}
	if p.Propose == nil || p.Audit == nil {
		p.status.Error = "Automatic proposals are not configured on this agent (no GitOps access)."
		return
	}
	version := result.Release.Version
	if err := p.Audit(map[string]any{"time": p.now().UTC(), "event": "release_proposal_requested", "version": version, "channel": settings.Channel, "manifest_hash": result.ManifestHash, "by": "automatic updates"}); err != nil {
		p.status.Error = "Cannot record the update proposal; nothing was changed."
		return
	}
	proposal, err := p.Propose(ctx, result.Release)
	if err != nil {
		_ = p.Audit(map[string]any{"time": p.now().UTC(), "event": "release_proposal_failed", "version": version, "branch": proposal.Branch, "by": "automatic updates"})
		p.status.Error = "Could not open the update pull request: " + err.Error()
		return
	}
	_ = p.Audit(map[string]any{"time": p.now().UTC(), "event": "release_proposal_created", "version": version, "branch": proposal.Branch, "by": "automatic updates"})
	p.data.Proposal = &Proposed{Version: version, URL: proposal.URL, Branch: proposal.Branch, At: p.now().UTC()}
	if err := p.save(); err != nil {
		// The pull request exists; failing to remember it risks a duplicate next time.
		p.status.Error = "The pull request was opened but could not be remembered: " + err.Error()
	}
}

// Run checks at start and then every checkEvery, until ctx ends.
func (p *Policy) Run(ctx context.Context) {
	p.mu.Lock()
	p.wake = make(chan struct{}, 1)
	wake := p.wake
	p.mu.Unlock()
	timer := time.NewTimer(30 * time.Second) // let the agent finish starting first
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-wake:
		}
		p.Check(ctx)
		timer.Reset(checkEvery)
	}
}

func (a API) registerPolicy(mux *http.ServeMux, protect func(http.Handler) http.Handler) {
	if a.Policy == nil {
		return
	}
	mux.Handle("GET /api/v1/releases/settings", protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.Policy.Status())
	})))
	mux.Handle("PUT /api/v1/releases/settings", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Settings
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "Invalid update settings", http.StatusBadRequest)
			return
		}
		if err := validSettings(request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		who := "operator"
		if a.Who != nil {
			if person := a.Who(r); person != "" {
				who = person
			}
		}
		if err := a.Policy.Set(request, who); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.Policy.Status())
	})))
}
