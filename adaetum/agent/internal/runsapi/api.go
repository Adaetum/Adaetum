// Package runsapi is the HTTP surface of the run engine (internal/runs): list the
// procedures an agent knows, start a run, watch it, retry it, cancel it.
//
// It follows the playbook launcher's pattern (internal/ansiblerun) on purpose, so
// the two powerful levers behave alike: everything but the capabilities sits behind
// a login, starting work can be switched off while reading and cancelling stay
// available, and the record names WHO started a run.
package runsapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// API serves the routes under /api/v1/runs.
type API struct {
	// Manager is nil when runs are unavailable on this agent (no local database).
	Manager *runs.Manager
	// Reason says why runs are unavailable, in words an operator can act on.
	Reason string
	// RequireUser lets only a signed-in person (or the access token) through.
	RequireUser func(http.Handler) http.Handler
	// Enabled, when set, says whether starting work is switched on right now. It
	// gates Start and Retry only; reading and cancelling are never blocked, so
	// switching it off stops new work without hiding or trapping a run in progress.
	Enabled func() (bool, string)
	// Who names the signed-in person making the request, or "" for the access token.
	Who func(*http.Request) string
}

const maxRequestBody = 16 * 1024

// Register adds the routes to mux.
func (api *API) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	// Capabilities only says whether runs exist and are switched on, so the page can
	// explain itself before anyone signs in. It lists no procedures.
	mux.HandleFunc("GET /api/v1/runs/capabilities", api.capabilities)
	mux.Handle("GET /api/v1/runs/procedures", user(http.HandlerFunc(api.procedures)))
	mux.Handle("GET /api/v1/runs", user(http.HandlerFunc(api.list)))
	mux.Handle("POST /api/v1/runs", user(http.HandlerFunc(api.start)))
	mux.Handle("GET /api/v1/runs/{id}", user(http.HandlerFunc(api.get)))
	mux.Handle("POST /api/v1/runs/{id}/retry", user(http.HandlerFunc(api.retry)))
	mux.Handle("POST /api/v1/runs/{id}/cancel", user(http.HandlerFunc(api.cancel)))
}

func (api *API) switchedOn() (bool, string) {
	if api.Manager == nil {
		return false, api.Reason
	}
	if api.Enabled != nil {
		return api.Enabled()
	}
	return true, ""
}

func (api *API) capabilities(w http.ResponseWriter, _ *http.Request) {
	on, reason := api.switchedOn()
	writeJSON(w, http.StatusOK, map[string]any{"available": api.Manager != nil, "enabled": on, "reason": reason})
}

func (api *API) procedures(w http.ResponseWriter, _ *http.Request) {
	list := []runs.ProcedureInfo{}
	if api.Manager != nil {
		list = api.Manager.Procedures()
	}
	writeJSON(w, http.StatusOK, map[string]any{"procedures": list})
}

func (api *API) list(w http.ResponseWriter, _ *http.Request) {
	list := []runs.Snapshot{}
	if api.Manager != nil {
		list = api.Manager.List()
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": list})
}

type startRequest struct {
	Procedure string            `json:"procedure"`
	Inputs    map[string]string `json:"inputs"`
	Approved  []string          `json:"approved"`
}

func (api *API) start(w http.ResponseWriter, r *http.Request) {
	if on, reason := api.switchedOn(); !on {
		writeError(w, http.StatusForbidden, reason)
		return
	}
	var request startRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not valid")
		return
	}
	id, err := api.Manager.Start(r.Context(), runs.StartRequest{
		Procedure: request.Procedure, Inputs: request.Inputs, Approved: request.Approved, StartedBy: api.who(r),
	})
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (api *API) get(w http.ResponseWriter, r *http.Request) {
	if api.Manager == nil {
		writeError(w, http.StatusNotFound, "no such run")
		return
	}
	id := r.PathValue("id")
	snap, ok := api.Manager.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no such run")
		return
	}
	// ?after=N returns only the events past the first N, so a page that polls does
	// not download the whole history each time.
	events := api.Manager.Events(id)
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	if after < 0 || after > len(events) {
		after = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": snap, "events": events[after:], "total": len(events)})
}

func (api *API) retry(w http.ResponseWriter, r *http.Request) {
	if on, reason := api.switchedOn(); !on {
		writeError(w, http.StatusForbidden, reason)
		return
	}
	var body struct {
		Approve []string `json:"approve"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "the request is not valid")
			return
		}
	}
	if err := api.Manager.Retry(r.Context(), r.PathValue("id"), api.who(r), body.Approve...); err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"retrying": true})
}

func (api *API) cancel(w http.ResponseWriter, r *http.Request) {
	if api.Manager == nil || !api.Manager.Cancel(r.PathValue("id")) {
		writeError(w, http.StatusConflict, "that run is not running")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"cancelling": true})
}

func (api *API) who(r *http.Request) string {
	if api.Who != nil {
		if person := api.Who(r); person != "" {
			return person
		}
	}
	return "operator"
}

// writeFailure maps the engine's error classes to HTTP statuses. Anything else is
// an internal failure, and its text is not shown: it may name files or sockets.
func writeFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runs.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, runs.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, runs.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "the run could not be recorded, so nothing was started")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
