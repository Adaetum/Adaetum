package cleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

const secret = "s3cret-token-value"

type apiHarness struct {
	api     *API
	server  *httptest.Server
	manager *managerHarness
	planErr error
	plans   int
	history string
}

func newAPI(t *testing.T, canApply bool, token string) *apiHarness {
	t.Helper()
	h := &apiHarness{manager: newManagerHarness(), history: filepath.Join(t.TempDir(), "audit.log")}
	api := &API{
		Capabilities: Capabilities{Host: "node-a", CanApply: canApply},
		Token:        token,
		HistoryPath:  h.history,
		Now:          func() time.Time { return now },
		Planner: func(_ context.Context, options inspect.PlanOptions) (inspect.CleanupPlan, error) {
			h.plans++
			if h.planErr != nil {
				return inspect.CleanupPlan{}, h.planErr
			}
			plan := planOf(item("a"), item("b"))
			plan.ID = "plan-" + string(rune('0'+h.plans))
			plan.Options = options
			return plan, nil
		},
	}
	if canApply {
		api.Manager = h.manager.manager
		api.Schedules = NewScheduleStore(filepath.Join(t.TempDir(), "schedule.json"), utc)
	} else {
		api.Capabilities.Reason = "This agent is reading node-a over SSH."
	}
	h.api = api
	mux := http.NewServeMux()
	api.Register(mux)
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

func (h *apiHarness) do(t *testing.T, method, path, token, body string, contentType ...string) (int, map[string]any) {
	t.Helper()
	request, _ := http.NewRequest(method, h.server.URL+path, bytes.NewBufferString(body))
	if len(contentType) == 0 {
		request.Header.Set("Content-Type", "application/json")
	} else if contentType[0] != "" {
		request.Header.Set("Content-Type", contentType[0])
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

func TestPreviewIsOpenButMakesAPlanTheServerRemembers(t *testing.T) {
	h := newAPI(t, true, secret)
	status, body := h.do(t, "POST", "/api/v1/images/plan", "", `{"includeOrphans":true,"allowRegistries":["ghcr.io"]}`)
	if status != 200 || body["id"] != "plan-1" {
		t.Fatalf("got %d %v", status, body)
	}
	if opts := h.api.plans["plan-1"].Options; !opts.IncludeOrphans || len(opts.AllowedRegistries) != 1 {
		t.Fatalf("the options must reach the planner: %+v", opts)
	}
}

func TestPreviewWorksEvenWhenChangesAreOff(t *testing.T) {
	h := newAPI(t, false, "")
	if status, _ := h.do(t, "POST", "/api/v1/images/plan", "", `{}`); status != 200 {
		t.Fatalf("a read-only agent must still preview, got %d", status)
	}
	status, body := h.do(t, "GET", "/api/v1/images/capabilities", "", "")
	if status != 200 || body["canApply"] != false || !strings.Contains(body["reason"].(string), "SSH") {
		t.Fatalf("the UI must be told why changes are off: %v", body)
	}
}

func TestBadRegistryNamesAreRejected(t *testing.T) {
	h := newAPI(t, true, secret)
	if status, _ := h.do(t, "POST", "/api/v1/images/plan", "", `{"allowRegistries":["x; rm -rf /"]}`); status != 400 {
		t.Fatalf("got %d", status)
	}
	if h.plans != 0 {
		t.Fatal("an invalid request must not reach the planner")
	}
}

func TestAPlanningFailureIsAnExplainedError(t *testing.T) {
	h := newAPI(t, true, secret)
	h.planErr = errors.New("cluster view unavailable, refusing to plan")
	status, body := h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	if status != http.StatusBadGateway || !strings.Contains(body["error"].(string), "refusing to plan") {
		t.Fatalf("got %d %v", status, body)
	}
}

func TestApplyNeedsTheToken(t *testing.T) {
	h := newAPI(t, true, secret)
	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	body := `{"planId":"plan-1"}`

	if status, _ := h.do(t, "POST", "/api/v1/images/apply", "", body); status != 401 {
		t.Fatalf("no token must be 401, got %d", status)
	}
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", "wrong", body); status != 401 {
		t.Fatalf("a wrong token must be 401, got %d", status)
	}
	if len(h.manager.removed) != 0 || h.manager.auditOpened != 0 {
		t.Fatal("nothing may start without a valid token")
	}
}

func TestApplyIsRefusedWhenNoTokenIsConfigured(t *testing.T) {
	h := newAPI(t, true, "")
	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", "anything", `{"planId":"plan-1"}`); status != 403 {
		t.Fatalf("with no configured token every apply must be refused, got %d", status)
	}
}

func TestApplyIsRefusedWhenThisAgentCannotChangeThings(t *testing.T) {
	// The dev setup: an agent reading a production host over SSH. Even a request
	// carrying a token must not be able to delete anything.
	h := newAPI(t, false, secret)
	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	status, body := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"plan-1"}`)
	if status != 403 || !strings.Contains(body["error"].(string), "SSH") {
		t.Fatalf("got %d %v", status, body)
	}
}

func TestApplyOnlyActsOnAPlanTheServerMade(t *testing.T) {
	h := newAPI(t, true, secret)
	status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"made-up-by-the-browser"}`)
	if status != 404 || len(h.manager.removed) != 0 {
		t.Fatalf("an unknown plan must be refused: %d", status)
	}
}

func TestAValidApplyStartsAJobAndThePlanCanOnlyBeUsedOnce(t *testing.T) {
	h := newAPI(t, true, secret)
	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)

	status, body := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"plan-1","maxImages":5}`)
	if status != http.StatusAccepted || body["jobId"] == nil {
		t.Fatalf("got %d %v", status, body)
	}
	h.manager.manager.Wait()

	status, job := h.do(t, "GET", "/api/v1/images/jobs/"+body["jobId"].(string), "", "")
	if status != 200 || job["state"] != "done" || job["trigger"] != "ui" {
		t.Fatalf("got %d %v", status, job)
	}
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"plan-1"}`); status != 404 {
		t.Fatalf("a plan must not be applicable twice, got %d", status)
	}
}

func TestAnExpiredPlanIsRefused(t *testing.T) {
	h := newAPI(t, true, secret)
	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	h.api.Now = func() time.Time { return now.Add(time.Hour) }
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"plan-1"}`); status != http.StatusGone {
		t.Fatalf("got %d", status)
	}
	if h.manager.auditOpened != 0 {
		t.Fatal("an expired plan must not start a run")
	}
}

func TestApplyWhileBusyIsAConflict(t *testing.T) {
	h := newAPI(t, true, secret)
	h.manager.gate = make(chan struct{})
	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"plan-1"}`); status != 202 {
		t.Fatalf("first apply: %d", status)
	}
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"plan-2"}`); status != http.StatusConflict {
		t.Fatalf("a second concurrent apply must be a conflict, got %d", status)
	}
	close(h.manager.gate)
	h.manager.manager.Wait()
}

func TestRequestShapeIsEnforced(t *testing.T) {
	h := newAPI(t, true, secret)
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `planId=plan-1`, "application/x-www-form-urlencoded"); status != 415 {
		t.Fatalf("a form post must be refused, got %d", status)
	}
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `{not json`); status != 400 {
		t.Fatalf("got %d", status)
	}
	big := `{"planId":"` + strings.Repeat("x", maxBody) + `"}`
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, big); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized body must be refused, got %d", status)
	}
	if status, _ := h.do(t, "POST", "/api/v1/images/apply", secret, `{"planId":"x","maxImages":9999}`); status != 400 {
		t.Fatalf("an absurd limit must be refused, got %d", status)
	}
}

func TestSchedulesAreSavedOnlyWithTheTokenAndValidated(t *testing.T) {
	h := newAPI(t, true, secret)
	good := `{"level":"aggressive","frequency":"weekly","weekday":0,"dayOfMonth":1,"time":"04:00","onlyIfDiskOverPercent":80,"includeOrphans":false,"allowRegistries":[],"maxImages":10,"checkOnly":false}`

	if status, _ := h.do(t, "PUT", "/api/v1/images/schedule", "", good); status != 401 {
		t.Fatalf("saving a schedule without the token must be 401, got %d", status)
	}
	if status, _ := h.do(t, "PUT", "/api/v1/images/schedule", secret, strings.Replace(good, "04:00", "99:00", 1)); status != 400 {
		t.Fatalf("an invalid schedule must be refused, got %d", status)
	}
	status, body := h.do(t, "PUT", "/api/v1/images/schedule", secret, good)
	if status != 200 || body["available"] != true {
		t.Fatalf("got %d %v", status, body)
	}
	status, body = h.do(t, "GET", "/api/v1/images/schedule", "", "")
	state := body["state"].(map[string]any)
	if status != 200 || state["schedule"].(map[string]any)["frequency"] != "weekly" || state["nextRun"] == nil {
		t.Fatalf("the saved schedule must be readable with its next run: %v", body)
	}
}

func TestScheduleIsUnavailableWhenChangesAreOff(t *testing.T) {
	h := newAPI(t, false, "")
	if _, body := h.do(t, "GET", "/api/v1/images/schedule", "", ""); body["available"] != false {
		t.Fatalf("got %v", body)
	}
	if status, _ := h.do(t, "PUT", "/api/v1/images/schedule", secret, `{}`); status != 403 {
		t.Fatalf("a read-only agent must refuse to save a schedule, got %d", status)
	}
}

func TestHistoryComesFromTheAuditLog(t *testing.T) {
	h := newAPI(t, true, secret)
	os.WriteFile(h.history, []byte(line("start", "p1", `,"trigger":"cli"`)+"\n"+line("end", "p1", `,"reason":"done"`)+"\n"), 0o640)
	request, _ := http.NewRequest("GET", h.server.URL+"/api/v1/images/history", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var runs []map[string]any
	_ = json.NewDecoder(response.Body).Decode(&runs)
	if len(runs) != 1 || runs[0]["trigger"] != "cli" {
		t.Fatalf("unexpected history: %v", runs)
	}
}

func TestTheAgentNeverAnswersCrossOriginPreflights(t *testing.T) {
	// Browsers need a successful preflight before a page on another origin may
	// send the Authorization header. Never granting one is what keeps a
	// malicious page from driving the delete endpoint.
	h := newAPI(t, true, secret)
	request, _ := http.NewRequest("OPTIONS", h.server.URL+"/api/v1/images/apply", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Access-Control-Allow-Origin") != "" || response.Header.Get("Access-Control-Allow-Headers") != "" {
		t.Fatal("the API must not grant cross-origin access")
	}
}

func TestPlansDefaultToAggressiveAndValidateTheLevel(t *testing.T) {
	h := newAPI(t, true, secret)

	h.do(t, "POST", "/api/v1/images/plan", "", `{}`)
	if level := h.api.plans["plan-1"].Options.Level; level != inspect.LevelAggressive {
		t.Fatalf("a plan request without a level must be aggressive, got %q", level)
	}
	h.do(t, "POST", "/api/v1/images/plan", "", `{"level":"careful"}`)
	if level := h.api.plans["plan-2"].Options.Level; level != inspect.LevelCareful {
		t.Fatalf("an explicit careful level must be honored, got %q", level)
	}
	if status, _ := h.do(t, "POST", "/api/v1/images/plan", "", `{"level":"reckless"}`); status != 400 {
		t.Fatalf("an unknown level must be refused, got %d", status)
	}
}

func TestAScheduleWithoutALevelIsRefused(t *testing.T) {
	h := newAPI(t, true, secret)
	noLevel := `{"frequency":"daily","weekday":0,"dayOfMonth":1,"time":"04:00","onlyIfDiskOverPercent":0,"includeOrphans":false,"allowRegistries":[],"maxImages":10,"checkOnly":false}`
	if status, _ := h.do(t, "PUT", "/api/v1/images/schedule", secret, noLevel); status != 400 {
		t.Fatalf("an ambiguous schedule must be refused, got %d", status)
	}
}
