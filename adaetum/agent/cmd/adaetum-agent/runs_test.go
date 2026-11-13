package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
	"github.com/Adaetum/Adaetum/agent/internal/runs"
	"github.com/Adaetum/Adaetum/agent/internal/runstore"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

func runsCall(mux *http.ServeMux, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func tinyProcedure() *runs.Procedure {
	done := false
	return &runs.Procedure{Name: "tiny", Steps: []runs.Step{{
		Name: "only",
		Check: func(context.Context, runs.Env) (runs.Status, error) {
			if done {
				return runs.Satisfied, nil
			}
			return runs.Unsatisfied, nil
		},
		Apply: func(context.Context, runs.Env) error { done = true; return nil },
	}}}
}

func openHostStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeToken(t *testing.T) (path, token string) {
	t.Helper()
	token = strings.Repeat("t", minTokenBytes+8)
	path = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, token
}

func TestRunsExplainThemselvesWithoutADatabase(t *testing.T) {
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, nil, &runsConfig{}, "", nil, nil, noBoard)
	defer stop()
	rec := runsCall(mux, "GET", "/api/v1/runs/capabilities", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "-data-dir") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRunsAreOffUntilTheAgentIsStartedWithTheSwitch(t *testing.T) {
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, openHostStore(t), &runsConfig{}, "", nil, []*runs.Procedure{tinyProcedure()}, noBoard)
	defer stop()
	rec := runsCall(mux, "POST", "/api/v1/runs", `{"procedure":"tiny"}`, "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "-allow-runs") {
		t.Fatalf("starting while off: %d %s", rec.Code, rec.Body)
	}
	if rec := runsCall(mux, "GET", "/api/v1/runs/procedures", "", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "tiny") {
		t.Fatalf("reading while off: %d %s", rec.Code, rec.Body)
	}
}

func TestStartingARunNeedsTheSwitchAndTheToken(t *testing.T) {
	tokenPath, token := writeToken(t)
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, openHostStore(t), &runsConfig{allow: true}, tokenPath, nil, []*runs.Procedure{tinyProcedure()}, noBoard)
	defer stop()

	// Even on a plain loopback agent with no other login, starting needs the token.
	if rec := runsCall(mux, "POST", "/api/v1/runs", `{"procedure":"tiny"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", rec.Code)
	}
	rec := runsCall(mux, "POST", "/api/v1/runs", `{"procedure":"tiny"}`, token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("with the token: %d %s", rec.Code, rec.Body)
	}
	var started struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &started)

	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := runsCall(mux, "GET", "/api/v1/runs/"+started.ID, "", token)
		if strings.Contains(rec.Body.String(), `"state":"succeeded"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run did not finish: %s", rec.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAMissingTokenFileKeepsStartingOffEvenWithTheSwitch(t *testing.T) {
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, openHostStore(t), &runsConfig{allow: true}, filepath.Join(t.TempDir(), "missing"), nil, []*runs.Procedure{tinyProcedure()}, noBoard)
	defer stop()
	rec := runsCall(mux, "GET", "/api/v1/runs/capabilities", "", "")
	if !strings.Contains(rec.Body.String(), `"enabled":false`) || !strings.Contains(rec.Body.String(), "access token") {
		t.Fatalf("%s", rec.Body)
	}
}

func TestAMalformedProcedureIsLeftOutNotFatal(t *testing.T) {
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, openHostStore(t), &runsConfig{}, "", nil, []*runs.Procedure{{Name: "broken"}, tinyProcedure()}, noBoard)
	defer stop()
	rec := runsCall(mux, "GET", "/api/v1/runs/procedures", "", "")
	if strings.Contains(rec.Body.String(), "broken") || !strings.Contains(rec.Body.String(), "tiny") {
		t.Fatalf("%s", rec.Body)
	}
}

func noBoard() *policy.Switchboard { return nil }

type memorySettings map[string]policy.Setting

func (m memorySettings) Setting(key string) (policy.Setting, bool, error) {
	setting, ok := m[key]
	return setting, ok, nil
}
func (m memorySettings) SaveSetting(setting policy.Setting) error {
	m[setting.Key] = setting
	return nil
}

func TestTheRunsSwitchIsOnTheSecurityPageAndFixedOnAHost(t *testing.T) {
	tokenPath, _ := writeToken(t)
	mux := http.NewServeMux()
	available, stop := setupRuns(mux, openHostStore(t), &runsConfig{allow: true}, tokenPath, nil, nil, noBoard)
	defer stop()

	board := policy.NewSwitchboard(nil, nil, runsLever(available, true, false))
	state, err := board.State(runsLeverKey)
	if err != nil {
		t.Fatal(err)
	}
	if state.Title != "Start runs" || state.Risk != "high" || state.Toggleable || !state.Available || !state.Enabled {
		t.Fatalf("a host agent shows the switch read-only and set from its start-up flag: %+v", state)
	}

	// Without a usable token the switch reads as unavailable and says why.
	mux2 := http.NewServeMux()
	missing, stop2 := setupRuns(mux2, openHostStore(t), &runsConfig{allow: true}, filepath.Join(t.TempDir(), "none"), nil, nil, noBoard)
	defer stop2()
	state, _ = policy.NewSwitchboard(nil, nil, runsLever(missing, true, false)).State(runsLeverKey)
	if state.Available || state.Enabled || !strings.Contains(state.Reason, "access token") {
		t.Fatalf("got %+v", state)
	}
}

func TestStartingFollowsTheSecurityPageSwitchWhenThereIsOne(t *testing.T) {
	tokenPath, token := writeToken(t)
	settings := memorySettings{}
	var board *policy.Switchboard
	mux := http.NewServeMux()
	available, stop := setupRuns(mux, openHostStore(t), &runsConfig{}, tokenPath, nil, []*runs.Procedure{tinyProcedure()}, func() *policy.Switchboard { return board })
	defer stop()
	board = policy.NewSwitchboard(settings, func(policy.AuditEntry) error { return nil }, runsLever(available, false, true))

	start := func() int { return runsCall(mux, "POST", "/api/v1/runs", `{"procedure":"tiny"}`, token).Code }
	if code := start(); code != http.StatusForbidden {
		t.Fatalf("starting while the switch is off: %d", code)
	}
	if rec := runsCall(mux, "GET", "/api/v1/runs", "", token); rec.Code != http.StatusOK {
		t.Fatalf("reading while the switch is off: %d", rec.Code)
	}

	if _, err := board.Set(runsLeverKey, policy.SetRequest{Enabled: true, Confirm: runsLeverKey, By: "ops@example.com"}); err != nil {
		t.Fatal(err)
	}
	if code := start(); code != http.StatusAccepted {
		t.Fatalf("starting after the switch is turned on: %d", code)
	}

	if _, err := board.Set(runsLeverKey, policy.SetRequest{Enabled: false, By: "ops@example.com"}); err != nil {
		t.Fatal(err)
	}
	if code := start(); code != http.StatusForbidden {
		t.Fatalf("starting after the switch is turned off again: %d", code)
	}

	// The switch cannot make starting work without a token: it is a prerequisite.
	if rec := runsCall(mux, "POST", "/api/v1/runs", `{"procedure":"tiny"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("starting without the token: %d", rec.Code)
	}
}

func TestTheHostAgentOffersEachFirstBootChunkAndTheWholeInOrder(t *testing.T) {
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, openHostStore(t), &runsConfig{}, "", nil, hostProcedures(inspect.NewInspector()), noBoard)
	defer stop()
	rec := runsCall(mux, "GET", "/api/v1/runs/procedures", "", "")
	var out struct{ Procedures []runs.ProcedureInfo }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byName := map[string][]string{}
	for _, procedure := range out.Procedures {
		for _, step := range procedure.Steps {
			byName[procedure.Name] = append(byName[procedure.Name], step.Name)
			if step.Kind == "destructive" {
				t.Errorf("%s/%s is destructive: nothing in first boot so far may be", procedure.Name, step.Name)
			}
		}
	}
	want := map[string]string{
		"host-readiness": "network,clock,disk",
		"intake":         "payload-format,payload-contract",
		"local-secrets":  "directory,generated,literals",
		"first-boot": "host-readiness/network,host-readiness/clock,host-readiness/disk," +
			"intake/payload-format,intake/payload-contract," +
			"local-secrets/directory,local-secrets/generated,local-secrets/literals",
	}
	for name, steps := range want {
		if got := strings.Join(byName[name], ","); got != steps {
			t.Errorf("%s: steps %q, want %q (a procedure that fails to register is left out, so this also catches a malformed one)", name, got, steps)
		}
	}
	if len(byName) != len(want) {
		t.Errorf("unexpected procedures: %v", byName)
	}
}

func listRuns(t *testing.T, mux *http.ServeMux) []runs.Snapshot {
	t.Helper()
	rec := runsCall(mux, "GET", "/api/v1/runs", "", "")
	var out struct{ Runs []runs.Snapshot }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Runs
}

func TestProceduresNamedAtStartAreStartedByTheAgentItself(t *testing.T) {
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, openHostStore(t), &runsConfig{atStart: "tiny, ,tiny-missing"}, "", nil, []*runs.Procedure{tinyProcedure()}, noBoard)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		list := listRuns(t, mux)
		if len(list) == 1 && list[0].State == runs.StateSucceeded {
			if list[0].StartedBy != "boot" || list[0].Procedure != "tiny" {
				t.Fatalf("got %+v", list[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the procedure was not started at boot (and the unknown name must not stop it): %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestARunResumedAtStartIsNotStartedASecondTime(t *testing.T) {
	db := openHostStore(t)
	// A run that was in progress when the agent last stopped.
	if err := (runstore.Journal{Store: db}).Append(context.Background(), runs.Record{
		RunID: "run_old", Procedure: "hangs", Kind: runs.KindRunStarted, At: time.Now(), StepNames: []string{"only"},
	}); err != nil {
		t.Fatal(err)
	}
	hangs := &runs.Procedure{Name: "hangs", Steps: []runs.Step{{
		Name:  "only",
		Check: func(context.Context, runs.Env) (runs.Status, error) { return runs.Unsatisfied, nil },
		Apply: func(ctx context.Context, _ runs.Env) error { <-ctx.Done(); return ctx.Err() },
	}}}
	mux := http.NewServeMux()
	_, stop := setupRuns(mux, db, &runsConfig{atStart: "hangs"}, "", nil, []*runs.Procedure{hangs}, noBoard)
	defer stop()
	list := listRuns(t, mux)
	if len(list) != 1 || list[0].RunID != "run_old" || list[0].State != runs.StateRunning {
		t.Fatalf("the resumed run should be the only one: %+v", list)
	}
}
