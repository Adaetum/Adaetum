// Package policy is the agent's security switchboard: the small set of powerful
// levers that can be turned on or off while the agent runs, from the web page.
//
// A LEVER is a capability that acts on servers (launching Ansible playbooks,
// running commands directly over SSH). Each lever is global: flipping it applies to
// every server at once, because the lever is the agent's willingness to act, not a
// property of one machine.
//
// HOW A LEVER'S STATE IS DECIDED (checked on every request, never cached):
//
//  1. It must be AVAILABLE: its prerequisites hold (an access token, the program it
//     needs, a writable audit file, not in development SSH mode). Nothing a person
//     does in the page can make an unavailable lever work.
//  2. Then the persisted setting decides: on, off, or on until a given time. When
//     nobody has ever flipped it, the startup flag decides (off unless the operator
//     started the agent with it).
//  3. A time-boxed "on" turns itself off when the time passes, with no one needing
//     to remember.
//
// WHAT PROTECTS A FLIP. Turning a lever ON needs a signed-in person, a typed
// confirmation naming the lever, and a record: the change is written to the
// security audit log BEFORE it takes effect, and if it cannot be written the lever
// does not change. Turning a lever OFF is always allowed and also recorded.
//
// This does not weaken the boundary in docs/security-boundary.md: it decides
// whether the cluster agent may use a lever it already has; it never gives a host
// agent a new one, and it never reaches into a host.
package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Lever describes one switchable capability.
type Lever struct {
	Key         string
	Title       string
	Description string
	// Risk is shown beside the switch ("high" levers get the stronger warning).
	Risk string
	// Available reports whether the prerequisites hold, and if not, why in words an
	// operator can act on. It is called on every check.
	Available func() (ok bool, reason string)
	// StartupDefault is what the operator asked for with a startup flag; it applies
	// until someone flips the lever.
	StartupDefault bool
	// Toggleable is false for levers that this agent can only set at startup.
	Toggleable bool
	// OffReason is the standing explanation shown while the lever is off.
	OffReason string
}

// Setting is the persisted decision for a lever.
type Setting struct {
	Key       string
	Enabled   bool
	Until     *time.Time // nil means until turned off
	ChangedBy string
	ChangedAt time.Time
}

// Store persists settings. The cluster agent's database implements it.
type Store interface {
	Setting(key string) (Setting, bool, error)
	SaveSetting(Setting) error
}

// AuditEntry records a flip.
type AuditEntry struct {
	Time    time.Time  `json:"time"`
	Lever   string     `json:"lever"`
	Enabled bool       `json:"enabled"`
	Until   *time.Time `json:"until,omitempty"`
	By      string     `json:"by"`
	// Via is how the change was made: "web" (the Security page), "mcp" (an AI
	// assistant through the agent's MCP server) or "assistant" (the one built into
	// the page).
	Via string `json:"via,omitempty"`
	// Reason is why, in the person's or assistant's own words. Required for MCP.
	Reason string `json:"reason,omitempty"`
}

// State is what the page shows for one lever.
type State struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
	Toggleable  bool   `json:"toggleable"`
	// Available is whether the prerequisites hold; Enabled is whether the lever is
	// actually usable right now (available and switched on).
	Available   bool       `json:"available"`
	Enabled     bool       `json:"enabled"`
	Reason      string     `json:"reason,omitempty"`
	Until       *time.Time `json:"until,omitempty"`
	ChangedBy   string     `json:"changedBy,omitempty"`
	ChangedAt   *time.Time `json:"changedAt,omitempty"`
	FromStartup bool       `json:"fromStartup"`
}

// Switchboard holds the levers.
type Switchboard struct {
	// Store may be nil (an agent with no database): levers are then fixed at startup.
	Store Store
	// Audit records a flip before it takes effect. If nil or failing, flips are refused.
	Audit func(AuditEntry) error
	Now   func() time.Time

	mu     sync.Mutex
	levers map[string]Lever
}

// NewSwitchboard returns a Switchboard with the given levers.
func NewSwitchboard(store Store, audit func(AuditEntry) error, levers ...Lever) *Switchboard {
	s := &Switchboard{Store: store, Audit: audit, Now: time.Now, levers: map[string]Lever{}}
	for _, lever := range levers {
		s.levers[lever.Key] = lever
	}
	return s
}

// ErrUnknownLever means no lever has that key.
var ErrUnknownLever = errors.New("there is no such setting")

// Enabled reports whether a lever is usable right now and, if not, why. It is what
// the launcher and the override ask on every request. Any doubt means off.
func (s *Switchboard) Enabled(key string) (bool, string) {
	state, err := s.state(key)
	if err != nil {
		return false, "The setting could not be read, so this stays off: " + err.Error()
	}
	return state.Enabled, state.Reason
}

// State describes one lever.
func (s *Switchboard) State(key string) (State, error) { return s.state(key) }

// List describes every lever, sorted by title.
func (s *Switchboard) List() []State {
	s.mu.Lock()
	keys := make([]string, 0, len(s.levers))
	for key := range s.levers {
		keys = append(keys, key)
	}
	s.mu.Unlock()

	states := make([]State, 0, len(keys))
	for _, key := range keys {
		if state, err := s.state(key); err == nil {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(a, b int) bool { return states[a].Title < states[b].Title })
	return states
}

func (s *Switchboard) lever(key string) (Lever, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lever, ok := s.levers[key]
	return lever, ok
}

func (s *Switchboard) state(key string) (State, error) {
	lever, ok := s.lever(key)
	if !ok {
		return State{}, ErrUnknownLever
	}
	state := State{Key: lever.Key, Title: lever.Title, Description: lever.Description, Risk: lever.Risk, Toggleable: lever.Toggleable && s.Store != nil}

	available, reason := lever.Available()
	state.Available = available

	enabled, fromStartup := lever.StartupDefault, true
	if s.Store != nil {
		setting, found, err := s.Store.Setting(key)
		if err != nil {
			return State{}, err
		}
		if found {
			enabled, fromStartup = setting.Enabled, false
			state.ChangedBy = setting.ChangedBy
			changed := setting.ChangedAt
			state.ChangedAt = &changed
			if setting.Until != nil {
				state.Until = setting.Until
				if !s.Now().Before(*setting.Until) {
					// A time-boxed "on" that has run out is simply off.
					enabled = false
					state.Until = nil
				}
			}
		}
	}
	state.FromStartup = fromStartup

	switch {
	case !available:
		state.Reason = reason
	case !enabled:
		state.Reason = lever.OffReason
	default:
		state.Enabled = true
	}
	return state, nil
}

// SetRequest asks to flip a lever.
type SetRequest struct {
	Enabled bool
	// Duration limits an "on" (zero means until turned off).
	Duration time.Duration
	// Confirm must equal the lever's key when turning a lever on: a deliberate act,
	// not a stray click or a scripted call.
	Confirm string
	By      string
	// Via is "web" (default), "mcp" or "assistant". An AI assistant is held to stricter rules than a
	// person at the page, because it cannot be shown the warning and typing the
	// confirmation costs it nothing: it must give a reason, and cannot turn a lever on
	// without a time limit.
	Via    string
	Reason string
}

// Ways a change can be made.
const (
	ViaWeb = "web"
	ViaMCP = "mcp"
	// ViaAssistant is the assistant built into the cluster agent's web page.
	ViaAssistant = "assistant"
)

const (
	minReasonLength = 10
	maxReasonLength = 500
	maxMCPDuration  = 24 * time.Hour
)

const maxDuration = 30 * 24 * time.Hour

// Set flips a lever. It records the change first and applies it only if the record
// was written.
func (s *Switchboard) Set(key string, request SetRequest) (State, error) {
	lever, ok := s.lever(key)
	if !ok {
		return State{}, ErrUnknownLever
	}
	if s.Store == nil || !lever.Toggleable {
		return State{}, errors.New("this setting can only be changed when the agent is started")
	}
	if request.Duration < 0 || request.Duration > maxDuration {
		return State{}, fmt.Errorf("a time limit may be at most %d days", int(maxDuration.Hours()/24))
	}
	via := request.Via
	if via == "" {
		via = ViaWeb
	}
	if via != ViaWeb && via != ViaMCP && via != ViaAssistant {
		return State{}, errors.New("unknown way of making this change")
	}
	reason := strings.TrimSpace(request.Reason)
	if len(reason) > maxReasonLength {
		return State{}, fmt.Errorf("the reason may be at most %d characters", maxReasonLength)
	}
	if via != ViaWeb && request.Enabled {
		// Turning a lever on through an assistant needs a stated reason (recorded, so a
		// person can see why) and a limit, so it cannot be left on and forgotten.
		if len(reason) < minReasonLength {
			return State{}, errors.New("say why this should be turned on (at least a short sentence); the reason is recorded")
		}
		if request.Duration <= 0 || request.Duration > maxMCPDuration {
			return State{}, errors.New("an assistant can turn this on for at most 24 hours, and must give a time limit")
		}
	}
	if request.Enabled {
		if available, why := lever.Available(); !available {
			return State{}, errors.New("this cannot be turned on: " + why)
		}
		if request.Confirm != key {
			return State{}, fmt.Errorf("to turn this on, type %q to confirm", key)
		}
	}
	if s.Audit == nil {
		return State{}, errors.New("changes cannot be recorded, so nothing was changed")
	}

	now := s.Now().UTC()
	setting := Setting{Key: key, Enabled: request.Enabled, ChangedBy: request.By, ChangedAt: now}
	if request.Enabled && request.Duration > 0 {
		until := now.Add(request.Duration)
		setting.Until = &until
	}
	if err := s.Audit(AuditEntry{Time: now, Lever: key, Enabled: setting.Enabled, Until: setting.Until, By: request.By, Via: via, Reason: reason}); err != nil {
		return State{}, fmt.Errorf("the change was not made because it could not be recorded: %w", err)
	}
	if err := s.Store.SaveSetting(setting); err != nil {
		return State{}, fmt.Errorf("the change could not be saved: %w", err)
	}
	return s.state(key)
}
