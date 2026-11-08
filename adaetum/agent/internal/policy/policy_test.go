package policy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu   sync.Mutex
	data map[string]Setting
	fail bool
}

func (m *memStore) Setting(key string) (Setting, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return Setting{}, false, errors.New("db down")
	}
	s, ok := m.data[key]
	return s, ok, nil
}

func (m *memStore) SaveSetting(s Setting) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]Setting{}
	}
	m.data[s.Key] = s
	return nil
}

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

type harness struct {
	board     *Switchboard
	store     *memStore
	audit     []AuditEntry
	auditFail bool
	available bool
	now       time.Time
}

func newHarness(startup bool) *harness {
	h := &harness{store: &memStore{}, available: true, now: t0}
	h.board = NewSwitchboard(h.store, func(e AuditEntry) error {
		if h.auditFail {
			return errors.New("disk full")
		}
		h.audit = append(h.audit, e)
		return nil
	}, Lever{
		Key: "host_ssh", Title: "Direct commands", Risk: "high", Toggleable: true, StartupDefault: startup,
		OffReason: "Direct commands are switched off.",
		Available: func() (bool, string) {
			if h.available {
				return true, ""
			}
			return false, "no access token"
		},
	})
	h.board.Now = func() time.Time { return h.now }
	return h
}

func on(confirm string, d time.Duration) SetRequest {
	return SetRequest{Enabled: true, Duration: d, Confirm: confirm, By: "operator"}
}

func TestALeverIsOffUntilSomeoneTurnsItOnUnlessTheStartupFlagSaidOtherwise(t *testing.T) {
	if enabled, reason := newHarness(false).board.Enabled("host_ssh"); enabled || !strings.Contains(reason, "switched off") {
		t.Fatalf("the default is off, with the reason: %v %q", enabled, reason)
	}
	if enabled, _ := newHarness(true).board.Enabled("host_ssh"); !enabled {
		t.Fatal("the startup flag turns it on until someone flips it")
	}
}

func TestFlippingAffectsEveryCheckImmediately(t *testing.T) {
	h := newHarness(false)
	if _, err := h.board.Set("host_ssh", on("host_ssh", 0)); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := h.board.Enabled("host_ssh"); !enabled {
		t.Fatal("it must be on at once")
	}
	if _, err := h.board.Set("host_ssh", SetRequest{Enabled: false, By: "operator"}); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := h.board.Enabled("host_ssh"); enabled {
		t.Fatal("turning it off must take effect at once")
	}
}

func TestAFlipOverridesTheStartupFlagBothWays(t *testing.T) {
	h := newHarness(true)
	h.board.Set("host_ssh", SetRequest{Enabled: false, By: "o"})
	if enabled, _ := h.board.Enabled("host_ssh"); enabled {
		t.Fatal("someone turned it off; the startup flag no longer decides")
	}
}

func TestTurningOnNeedsTheLeversNameTyped(t *testing.T) {
	h := newHarness(false)
	for _, confirm := range []string{"", "yes", "HOST_SSH", "host_ssh "} {
		if _, err := h.board.Set("host_ssh", on(confirm, 0)); err == nil {
			t.Errorf("confirmation %q must be refused", confirm)
		}
	}
	if len(h.audit) != 0 {
		t.Fatal("a refused flip must leave no record of a change")
	}
	if _, err := h.board.Set("host_ssh", SetRequest{Enabled: false}); err != nil {
		t.Fatalf("turning OFF never needs a confirmation: %v", err)
	}
}

func TestAnUnavailableLeverCannotBeTurnedOnAndStaysOffEvenIfOnceEnabled(t *testing.T) {
	h := newHarness(false)
	h.board.Set("host_ssh", on("host_ssh", 0))
	h.available = false // the token went away
	if enabled, reason := h.board.Enabled("host_ssh"); enabled || !strings.Contains(reason, "no access token") {
		t.Fatalf("prerequisites are checked on every request: %v %q", enabled, reason)
	}
	if _, err := h.board.Set("host_ssh", on("host_ssh", 0)); err == nil {
		t.Fatal("a lever whose prerequisites fail cannot be switched on from the page")
	}
}

func TestATimeBoxedOnTurnsItselfOff(t *testing.T) {
	h := newHarness(false)
	state, _ := h.board.Set("host_ssh", on("host_ssh", time.Hour))
	if state.Until == nil || !state.Until.Equal(t0.Add(time.Hour)) {
		t.Fatalf("the end time must be reported: %+v", state)
	}
	h.now = t0.Add(59 * time.Minute)
	if enabled, _ := h.board.Enabled("host_ssh"); !enabled {
		t.Fatal("still on inside the window")
	}
	h.now = t0.Add(time.Hour)
	if enabled, reason := h.board.Enabled("host_ssh"); enabled || reason == "" {
		t.Fatalf("it must switch itself off at the end: %v %q", enabled, reason)
	}
}

func TestAFlipIsRecordedBeforeItTakesEffectAndRefusedIfItCannotBe(t *testing.T) {
	h := newHarness(false)
	h.auditFail = true
	if _, err := h.board.Set("host_ssh", on("host_ssh", 0)); err == nil {
		t.Fatal("an unrecorded change is what the record exists to prevent")
	}
	if enabled, _ := h.board.Enabled("host_ssh"); enabled {
		t.Fatal("nothing may have changed")
	}
	h.auditFail = false
	h.board.Set("host_ssh", on("host_ssh", time.Hour))
	if len(h.audit) != 1 || h.audit[0].Lever != "host_ssh" || !h.audit[0].Enabled || h.audit[0].By != "operator" || h.audit[0].Until == nil {
		t.Fatalf("who, what, until when: %+v", h.audit)
	}
}

func TestLimitsOnTheTimeBox(t *testing.T) {
	h := newHarness(false)
	if _, err := h.board.Set("host_ssh", on("host_ssh", -time.Hour)); err == nil {
		t.Error("a negative time limit is meaningless")
	}
	if _, err := h.board.Set("host_ssh", on("host_ssh", 31*24*time.Hour)); err == nil {
		t.Error("an unbounded 'temporary' setting defeats the point")
	}
}

func TestAnUnreadableSettingMeansOff(t *testing.T) {
	h := newHarness(true)
	h.store.fail = true
	if enabled, reason := h.board.Enabled("host_ssh"); enabled || reason == "" {
		t.Fatalf("when the setting cannot be read, the answer is no, even if the startup flag said yes: %v %q", enabled, reason)
	}
}

func TestWithoutAStoreTheLeverIsFixedAtStartup(t *testing.T) {
	b := NewSwitchboard(nil, nil, Lever{Key: "k", Title: "K", Toggleable: true, StartupDefault: true, Available: func() (bool, string) { return true, "" }})
	if enabled, _ := b.Enabled("k"); !enabled {
		t.Fatal("the startup default applies")
	}
	if _, err := b.Set("k", SetRequest{Enabled: false}); err == nil {
		t.Fatal("an agent with nowhere to keep a change cannot change")
	}
	if state, _ := b.State("k"); state.Toggleable {
		t.Fatal("the page must not offer a switch that cannot work")
	}
}

func TestAnUnknownLeverIsAnError(t *testing.T) {
	h := newHarness(false)
	if _, err := h.board.Set("rm_rf", on("rm_rf", 0)); !errors.Is(err, ErrUnknownLever) {
		t.Fatal("expected ErrUnknownLever")
	}
	if enabled, _ := h.board.Enabled("rm_rf"); enabled {
		t.Fatal("an unknown lever is never on")
	}
}

func TestTheAPINeedsALoginAndExplainsRefusals(t *testing.T) {
	h := newHarness(false)
	api := &API{Board: h.board, RequireUser: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ok" {
				http.Error(w, "login", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}}
	mux := http.NewServeMux()
	api.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	do := func(method, path, body, token string) (int, string) {
		request, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 4096)
		n, _ := response.Body.Read(buf)
		sb.Write(buf[:n])
		return response.StatusCode, sb.String()
	}

	if code, _ := do("GET", "/api/v1/security", "", ""); code != 401 {
		t.Fatalf("listing needs a login: %d", code)
	}
	if code, _ := do("PUT", "/api/v1/security/host_ssh", `{"enabled":true,"confirm":"host_ssh"}`, ""); code != 401 {
		t.Fatalf("flipping needs a login: %d", code)
	}
	if code, body := do("PUT", "/api/v1/security/host_ssh", `{"enabled":true}`, "ok"); code != 400 || !strings.Contains(body, "type") {
		t.Fatalf("a missing confirmation must say what to type: %d %s", code, body)
	}
	if code, _ := do("PUT", "/api/v1/security/nope", `{"enabled":true,"confirm":"nope"}`, "ok"); code != 404 {
		t.Fatalf("unknown lever: %d", code)
	}
	if code, body := do("PUT", "/api/v1/security/host_ssh", `{"enabled":true,"durationMinutes":60,"confirm":"host_ssh"}`, "ok"); code != 200 || !strings.Contains(body, `"enabled":true`) {
		t.Fatalf("a proper flip works: %d %s", code, body)
	}
	if code, body := do("GET", "/api/v1/security", "", "ok"); code != 200 || !strings.Contains(body, "host_ssh") {
		t.Fatalf("%d %s", code, body)
	}
}

func mcpOn(reason string, d time.Duration) SetRequest {
	return SetRequest{Enabled: true, Duration: d, Confirm: "host_ssh", By: "operator", Via: ViaMCP, Reason: reason}
}

func TestAnAssistantMustGiveAReasonToTurnSomethingOn(t *testing.T) {
	h := newHarness(false)
	for _, reason := range []string{"", "   ", "because"} {
		if _, err := h.board.Set("host_ssh", mcpOn(reason, time.Hour)); err == nil {
			t.Errorf("reason %q is not a reason", reason)
		}
	}
	if len(h.audit) != 0 {
		t.Fatal("a refused change leaves no record of a change")
	}
}

func TestAnAssistantMustGiveATimeLimitOfAtMostADay(t *testing.T) {
	h := newHarness(false)
	for _, d := range []time.Duration{0, -time.Hour, 25 * time.Hour, 24*time.Hour + time.Minute} {
		if _, err := h.board.Set("host_ssh", mcpOn("fixing the broken node by hand", d)); err == nil {
			t.Errorf("duration %v must be refused for an assistant", d)
		}
	}
	if _, err := h.board.Set("host_ssh", mcpOn("fixing the broken node by hand", 24*time.Hour)); err != nil {
		t.Fatalf("exactly a day is allowed: %v", err)
	}
}

func TestAPersonAtThePageIsNotHeldToTheAssistantsLimits(t *testing.T) {
	h := newHarness(false)
	if _, err := h.board.Set("host_ssh", SetRequest{Enabled: true, Confirm: "host_ssh", By: "operator", Via: ViaWeb}); err != nil {
		t.Fatalf("a person may choose 'until I turn it off' without a reason: %v", err)
	}
}

func TestTurningOffThroughAnAssistantNeedsNeitherReasonNorLimit(t *testing.T) {
	h := newHarness(true)
	if _, err := h.board.Set("host_ssh", SetRequest{Enabled: false, By: "operator", Via: ViaMCP}); err != nil {
		t.Fatalf("the safe direction is always open: %v", err)
	}
	if enabled, _ := h.board.Enabled("host_ssh"); enabled {
		t.Fatal("it must be off")
	}
}

func TestTheRecordSaysItWasMadeThroughMCPAndWhy(t *testing.T) {
	h := newHarness(false)
	h.board.Set("host_ssh", mcpOn("checking the disk on node-a for the user", time.Hour))
	if len(h.audit) != 1 || h.audit[0].Via != ViaMCP || h.audit[0].Reason != "checking the disk on node-a for the user" {
		t.Fatalf("who asked, how, and why must be recorded: %+v", h.audit)
	}
}

func TestAnUnknownWayOfChangingIsRefused(t *testing.T) {
	h := newHarness(false)
	if _, err := h.board.Set("host_ssh", SetRequest{Enabled: false, Via: "telepathy"}); err == nil {
		t.Fatal("an unrecognised source must not be recorded as if it were valid")
	}
}

// The record must say WHICH person flipped a switch when someone signed in with an
// account, and fall back to "operator" for the shared access token.
func TestTheRecordNamesThePersonWhoFlippedTheSwitch(t *testing.T) {
	h := newHarness(false)
	mux := http.NewServeMux()
	(&API{Board: h.board, Who: func(r *http.Request) string { return r.Header.Get("X-Test-Person") }}).Register(mux)

	flip := func(person string) {
		request := httptest.NewRequest("PUT", "/api/v1/security/host_ssh", strings.NewReader(`{"enabled":true,"durationMinutes":60,"confirm":"host_ssh"}`))
		if person != "" {
			request.Header.Set("X-Test-Person", person)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("flip: %d %s", recorder.Code, recorder.Body.String())
		}
	}
	flip("sam@example.test")
	flip("") // the access token: no person
	if len(h.audit) != 2 || h.audit[0].By != "sam@example.test" || h.audit[1].By != "operator" {
		t.Fatalf("the record names %+v", h.audit)
	}
}
