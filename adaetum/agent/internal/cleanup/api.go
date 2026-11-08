package cleanup

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

// Capabilities tell the UI what this agent may do, so it can show a working
// button or an honest explanation. Preview is always available; changing
// anything is only on when the agent runs on the server itself, as root, with an
// access token.
type Capabilities struct {
	Host     string `json:"host"`
	CanApply bool   `json:"canApply"`
	// Reason says why changes are off, in a sentence for the operator.
	Reason string `json:"reason,omitempty"`
	// Hint is the command to run on the server when the UI cannot apply.
	Hint string `json:"hint,omitempty"`
	// DefaultRegistries are the registries a plan may remove images from unless
	// the operator allows more. Reported so the UI never has its own copy.
	DefaultRegistries []string `json:"defaultRegistries"`
}

// API serves the cleanup endpoints.
//
// Authentication: previews and reads are open, like the rest of the agent's read
// API. Anything that deletes or changes the schedule needs the bearer token, and
// is refused outright when no token is configured. Cross-site pages cannot send
// the Authorization header without a preflight, which this API never grants.
type API struct {
	Capabilities Capabilities
	Planner      func(ctx context.Context, options inspect.PlanOptions) (inspect.CleanupPlan, error)
	// Manager and Schedules are nil when changes are off.
	Manager     *Manager
	Schedules   *ScheduleStore
	HistoryPath string
	// Token is the access token; empty means changes are off.
	Token string
	Now   func() time.Time

	mu    sync.Mutex
	plans map[string]inspect.CleanupPlan
	order []string
}

const (
	maxBody     = 1 << 20
	keptPlans   = 10
	planTimeout = 90 * time.Second
)

func (api *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/images/capabilities", api.capabilities)
	mux.HandleFunc("POST /api/v1/images/plan", api.plan)
	mux.HandleFunc("POST /api/v1/images/apply", api.apply)
	mux.HandleFunc("POST /api/v1/images/cancel", api.cancel)
	mux.HandleFunc("GET /api/v1/images/jobs/latest", api.latestJob)
	mux.HandleFunc("GET /api/v1/images/jobs/{id}", api.job)
	mux.HandleFunc("GET /api/v1/images/history", api.history)
	mux.HandleFunc("GET /api/v1/images/schedule", api.getSchedule)
	mux.HandleFunc("PUT /api/v1/images/schedule", api.putSchedule)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// decode reads a small JSON body. Requiring the JSON content type keeps plain
// HTML form posts from another site from being accepted.
func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Send JSON.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "That request is too large.")
		} else {
			writeError(w, http.StatusBadRequest, "That request could not be read.")
		}
		return false
	}
	return true
}

// authorize guards everything that changes the server. It returns false after
// writing the response.
func (api *API) authorize(w http.ResponseWriter, r *http.Request) bool {
	if !api.Capabilities.CanApply || api.Manager == nil {
		writeError(w, http.StatusForbidden, api.Capabilities.Reason)
		return false
	}
	if api.Token == "" {
		writeError(w, http.StatusForbidden, "Changes are switched off because the agent has no access token.")
		return false
	}
	presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	// Compared in constant time so the token cannot be guessed from response timing.
	if presented == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(presented), []byte(api.Token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="adaetum-agent"`)
		writeError(w, http.StatusUnauthorized, "The access token is missing or wrong.")
		return false
	}
	return true
}

func (api *API) capabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, api.Capabilities)
}

type planRequest struct {
	// Level is careful or aggressive; empty means aggressive.
	Level           string   `json:"level"`
	IncludeOrphans  bool     `json:"includeOrphans"`
	AllowRegistries []string `json:"allowRegistries"`
}

// plan builds a preview. It is read-only. The plan is kept here, and apply will
// only ever act on a plan the server itself made, never on one the browser sends.
func (api *API) plan(w http.ResponseWriter, r *http.Request) {
	var request planRequest
	if !decode(w, r, &request) {
		return
	}
	if err := ValidateRegistries(request.AllowRegistries); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	level := request.Level
	if level == "" {
		level = inspect.LevelAggressive
	}
	if err := ValidateLevel(level); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), planTimeout)
	defer cancel()
	plan, err := api.Planner(ctx, inspect.PlanOptions{Level: level, AllowedRegistries: request.AllowRegistries, IncludeOrphans: request.IncludeOrphans})
	if err != nil {
		writeError(w, http.StatusBadGateway, "A cleanup plan could not be made: "+err.Error())
		return
	}
	api.remember(plan)
	writeJSON(w, http.StatusOK, plan)
}

func (api *API) remember(plan inspect.CleanupPlan) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.plans == nil {
		api.plans = map[string]inspect.CleanupPlan{}
	}
	api.plans[plan.ID] = plan
	api.order = append(api.order, plan.ID)
	for len(api.order) > keptPlans {
		delete(api.plans, api.order[0])
		api.order = api.order[1:]
	}
}

// take returns a remembered plan and forgets it, so a plan can be applied once.
func (api *API) take(id string) (inspect.CleanupPlan, bool) {
	api.mu.Lock()
	defer api.mu.Unlock()
	plan, ok := api.plans[id]
	if ok {
		delete(api.plans, id)
	}
	return plan, ok
}

type applyRequest struct {
	PlanID    string `json:"planId"`
	MaxImages int    `json:"maxImages"`
	DryRun    bool   `json:"dryRun"`
}

func (api *API) apply(w http.ResponseWriter, r *http.Request) {
	if !api.authorize(w, r) {
		return
	}
	var request applyRequest
	if !decode(w, r, &request) {
		return
	}
	if request.MaxImages == 0 {
		request.MaxImages = defaultMaxImages
	}
	if request.MaxImages < 1 || request.MaxImages > maxScheduledMax {
		writeError(w, http.StatusBadRequest, "Images per run must be between 1 and 200.")
		return
	}
	plan, ok := api.take(request.PlanID)
	if !ok {
		writeError(w, http.StatusNotFound, "That plan is not available any more. Review the cleanup again.")
		return
	}
	if api.Now().After(plan.ExpiresAt) {
		writeError(w, http.StatusGone, "That plan has expired. Review the cleanup again.")
		return
	}
	jobID, err := api.Manager.Start(plan, Options{DryRun: request.DryRun, MaxImages: request.MaxImages, Trigger: "ui"})
	if errors.Is(err, ErrBusy) {
		writeError(w, http.StatusConflict, "Another cleanup is already running.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

func (api *API) cancel(w http.ResponseWriter, r *http.Request) {
	if !api.authorize(w, r) {
		return
	}
	api.Manager.Cancel()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (api *API) latestJob(w http.ResponseWriter, _ *http.Request) {
	if api.Manager == nil {
		writeError(w, http.StatusNotFound, "No cleanup has run.")
		return
	}
	job, ok := api.Manager.Latest()
	if !ok {
		writeError(w, http.StatusNotFound, "No cleanup has run.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (api *API) job(w http.ResponseWriter, r *http.Request) {
	if api.Manager == nil {
		writeError(w, http.StatusNotFound, "No such cleanup.")
		return
	}
	job, ok := api.Manager.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "No such cleanup.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (api *API) history(w http.ResponseWriter, _ *http.Request) {
	runs, err := ReadHistory(api.HistoryPath, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The cleanup history could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// scheduleView is the schedule plus whether it can be used.
type scheduleView struct {
	Available bool          `json:"available"`
	State     ScheduleState `json:"state"`
}

func (api *API) getSchedule(w http.ResponseWriter, _ *http.Request) {
	if api.Schedules == nil {
		writeJSON(w, http.StatusOK, scheduleView{Available: false, State: ScheduleState{Schedule: DefaultSchedule()}})
		return
	}
	state, err := api.Schedules.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scheduleView{Available: true, State: state})
}

func (api *API) putSchedule(w http.ResponseWriter, r *http.Request) {
	if !api.authorize(w, r) {
		return
	}
	if api.Schedules == nil {
		writeError(w, http.StatusForbidden, "Schedules are not available on this agent.")
		return
	}
	var schedule Schedule
	if !decode(w, r, &schedule) {
		return
	}
	state, err := api.Schedules.SetSchedule(schedule, api.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scheduleView{Available: true, State: state})
}
