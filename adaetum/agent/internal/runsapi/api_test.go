package runsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

func newAPI(t *testing.T, mods ...func(*API)) (*API, *http.ServeMux) {
	t.Helper()
	m, err := runs.NewManager(runs.Options{Journal: &runs.MemoryJournal{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	built, burned := false, false
	err = m.Register(&runs.Procedure{Name: "up", Description: "bring a thing up", Steps: []runs.Step{
		{
			Name: "thing",
			Check: func(context.Context, runs.Env) (runs.Status, error) {
				if built {
					return runs.Satisfied, nil
				}
				return runs.Unsatisfied, nil
			},
			Apply: func(context.Context, runs.Env) error { built = true; return nil },
		},
		{
			Name: "burn", Needs: []string{"thing"}, Kind: runs.Destructive,
			Check: func(context.Context, runs.Env) (runs.Status, error) {
				if burned {
					return runs.Satisfied, nil
				}
				return runs.Unsatisfied, nil
			},
			Apply: func(context.Context, runs.Env) error { burned = true; return nil },
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{Manager: m}
	for _, mod := range mods {
		mod(api)
	}
	mux := http.NewServeMux()
	api.Register(mux)
	return api, mux
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("bad JSON %q: %v", rec.Body.String(), err)
	}
}

func startRun(t *testing.T, api *API, mux *http.ServeMux, body string) string {
	t.Helper()
	rec := do(mux, "POST", "/api/v1/runs", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	var out struct{ ID string }
	decode(t, rec, &out)
	if err := api.Manager.Wait(context.Background(), out.ID); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func TestStartThenWatchARun(t *testing.T) {
	api, mux := newAPI(t)
	id := startRun(t, api, mux, `{"procedure":"up","inputs":{"node":"node-1"}}`)

	rec := do(mux, "GET", "/api/v1/runs/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Run    runs.Snapshot
		Events []runs.Record
		Total  int
	}
	decode(t, rec, &got)
	// The destructive step has no approval, so the run stops for a person.
	if got.Run.State != runs.StateNeedsHuman || got.Run.Code != runs.CodeApproval || got.Run.StartedBy != "operator" {
		t.Fatalf("got %+v", got.Run)
	}
	if got.Total == 0 || len(got.Events) != got.Total {
		t.Fatalf("events %d of %d", len(got.Events), got.Total)
	}

	// ?after returns only what is new.
	rec = do(mux, "GET", "/api/v1/runs/"+id+"?after="+itoa(got.Total-1), "")
	var tail struct{ Events []runs.Record }
	decode(t, rec, &tail)
	if len(tail.Events) != 1 {
		t.Fatalf("after=%d should return the last event only, got %d", got.Total-1, len(tail.Events))
	}

	rec = do(mux, "GET", "/api/v1/runs", "")
	var list struct{ Runs []runs.Snapshot }
	decode(t, rec, &list)
	if len(list.Runs) != 1 || list.Runs[0].RunID != id {
		t.Fatalf("list: %+v", list.Runs)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestRetryWithAnApprovalFinishesTheRun(t *testing.T) {
	api, mux := newAPI(t)
	id := startRun(t, api, mux, `{"procedure":"up"}`)
	rec := do(mux, "POST", "/api/v1/runs/"+id+"/retry", `{"approve":["burn"]}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	if err := api.Manager.Wait(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	snap, _ := api.Manager.Get(id)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s", snap.State)
	}
	// A finished run cannot be retried.
	if rec := do(mux, "POST", "/api/v1/runs/"+id+"/retry", ""); rec.Code != http.StatusConflict {
		t.Fatalf("retrying a finished run: %d", rec.Code)
	}
}

func TestMistakesGetTheRightStatus(t *testing.T) {
	_, mux := newAPI(t)
	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"unknown procedure", "POST", "/api/v1/runs", `{"procedure":"nope"}`, http.StatusBadRequest},
		{"secret input", "POST", "/api/v1/runs", `{"procedure":"up","inputs":{"admin_password":"x"}}`, http.StatusBadRequest},
		{"approving a plain step", "POST", "/api/v1/runs", `{"procedure":"up","approved":["thing"]}`, http.StatusBadRequest},
		{"garbage body", "POST", "/api/v1/runs", `{not json`, http.StatusBadRequest},
		{"unknown run", "GET", "/api/v1/runs/run_nope", "", http.StatusNotFound},
		{"retry unknown run", "POST", "/api/v1/runs/run_nope/retry", "", http.StatusNotFound},
		{"cancel unknown run", "POST", "/api/v1/runs/run_nope/cancel", "", http.StatusConflict},
	}
	for _, c := range cases {
		if rec := do(mux, c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s: got %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
	}
}

func TestSwitchedOffBlocksStartingButNotReading(t *testing.T) {
	_, mux := newAPI(t, func(a *API) {
		a.Enabled = func() (bool, string) { return false, "switched off in Security settings" }
	})
	rec := do(mux, "POST", "/api/v1/runs", `{"procedure":"up"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "switched off") {
		t.Fatalf("start while off: %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, "POST", "/api/v1/runs/run_x/retry", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("retry while off: %d", rec.Code)
	}
	for _, path := range []string{"/api/v1/runs", "/api/v1/runs/procedures", "/api/v1/runs/capabilities"} {
		if rec := do(mux, "GET", path, ""); rec.Code != http.StatusOK {
			t.Errorf("reading %s while off: %d", path, rec.Code)
		}
	}
}

func TestEverythingButCapabilitiesNeedsALogin(t *testing.T) {
	deny := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "sign in", http.StatusUnauthorized) })
	}
	_, mux := newAPI(t, func(a *API) { a.RequireUser = deny })
	for _, c := range [][2]string{
		{"GET", "/api/v1/runs"}, {"POST", "/api/v1/runs"}, {"GET", "/api/v1/runs/procedures"},
		{"GET", "/api/v1/runs/run_1"}, {"POST", "/api/v1/runs/run_1/retry"}, {"POST", "/api/v1/runs/run_1/cancel"},
	} {
		if rec := do(mux, c[0], c[1], `{}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a login: %d", c[0], c[1], rec.Code)
		}
	}
	rec := do(mux, "GET", "/api/v1/runs/capabilities", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "up") {
		t.Fatalf("capabilities should be open and list no procedures: %d %s", rec.Code, rec.Body)
	}
}

func TestTheRecordNamesTheSignedInPerson(t *testing.T) {
	api, mux := newAPI(t, func(a *API) {
		a.Who = func(*http.Request) string { return "ops@example.com" }
	})
	id := startRun(t, api, mux, `{"procedure":"up"}`)
	if snap, _ := api.Manager.Get(id); snap.StartedBy != "ops@example.com" {
		t.Fatalf("started by %q", snap.StartedBy)
	}
}

func TestUnavailableOnAnAgentWithoutADatabase(t *testing.T) {
	mux := http.NewServeMux()
	(&API{Reason: "runs need -data-dir"}).Register(mux)
	rec := do(mux, "GET", "/api/v1/runs/capabilities", "")
	var caps struct {
		Available, Enabled bool
		Reason             string
	}
	decode(t, rec, &caps)
	if caps.Available || caps.Enabled || !strings.Contains(caps.Reason, "-data-dir") {
		t.Fatalf("got %+v", caps)
	}
	if rec := do(mux, "POST", "/api/v1/runs", `{"procedure":"up"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("start without a manager: %d", rec.Code)
	}
	if rec := do(mux, "GET", "/api/v1/runs", ""); rec.Code != http.StatusOK {
		t.Fatalf("list without a manager: %d", rec.Code)
	}
}

func TestProceduresAreListed(t *testing.T) {
	_, mux := newAPI(t)
	rec := do(mux, "GET", "/api/v1/runs/procedures", "")
	var out struct{ Procedures []runs.ProcedureInfo }
	decode(t, rec, &out)
	if len(out.Procedures) != 1 || out.Procedures[0].Name != "up" || len(out.Procedures[0].Steps) != 2 || out.Procedures[0].Steps[1].Kind != "destructive" {
		t.Fatalf("got %+v", out.Procedures)
	}
}
