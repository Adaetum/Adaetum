package ansiblerun

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/auditlog"
)

// Capabilities says whether this agent may launch playbooks, and if not, why, in
// words an operator can act on (the same pattern as image cleanup). The catalog is
// included so the page can build its form.
type Capabilities struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	// Controller names the machine Ansible runs on.
	Controller string     `json:"controller,omitempty"`
	Catalog    []Playbook `json:"catalog"`
}

// API is the HTTP surface of the launcher.
type API struct {
	// Manager is nil when the launcher is disabled.
	Manager      *Manager
	Capabilities Capabilities
	// RequireUser lets only a signed-in person through. Everything that starts,
	// reads, or stops a run sits behind it; the capabilities (which only describe
	// what could be run) do not.
	RequireUser func(http.Handler) http.Handler
	// Enabled, when set, is asked on every request whether launching is switched on
	// right now (the Security page's switch). The launcher can be available (Manager
	// exists) but switched off; the answer includes why.
	Enabled func() (bool, string)
	// Who names the signed-in person who launched a run (their email), or "" for the
	// access token. The record then says WHICH person started it, not just "operator".
	Who func(*http.Request) string
}

const maxRequestBody = 16 * 1024

// Register adds the routes to mux.
func (api *API) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	mux.HandleFunc("GET /api/v1/ansible/capabilities", api.capabilities)
	mux.Handle("GET /api/v1/ansible/runs", user(http.HandlerFunc(api.list)))
	mux.Handle("POST /api/v1/ansible/runs", user(http.HandlerFunc(api.start)))
	mux.Handle("GET /api/v1/ansible/runs/{id}", user(http.HandlerFunc(api.get)))
	mux.Handle("POST /api/v1/ansible/runs/{id}/cancel", user(http.HandlerFunc(api.cancel)))
}

func (api *API) capabilities(w http.ResponseWriter, _ *http.Request) {
	capabilities := api.Capabilities
	if capabilities.Catalog == nil {
		capabilities.Catalog = Catalog()
	}
	capabilities.Enabled, capabilities.Reason = api.switchedOn()
	writeJSON(w, http.StatusOK, capabilities)
}

func (api *API) start(w http.ResponseWriter, r *http.Request) {
	if on, reason := api.switchedOn(); !on {
		writeError(w, http.StatusForbidden, reason)
		return
	}
	var request Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not valid")
		return
	}
	if _, err := Validate(request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	by := "operator"
	if api.Who != nil {
		if person := api.Who(r); person != "" {
			by = person
		}
	}
	id, err := api.Manager.Start(request, by)
	switch {
	case errors.Is(err, ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
	}
}

// switchedOn is whether a launch may start now: the launcher must exist and, if a
// switch is wired, be on. Cancelling and reading are never blocked by the switch, so
// turning it off stops new launches without hiding or trapping one in progress.
func (api *API) switchedOn() (bool, string) {
	if api.Manager == nil {
		return false, api.Capabilities.Reason
	}
	if api.Enabled != nil {
		return api.Enabled()
	}
	return true, ""
}

func (api *API) list(w http.ResponseWriter, _ *http.Request) {
	runs := []Run{}
	if api.Manager != nil {
		runs = api.Manager.List()
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (api *API) get(w http.ResponseWriter, r *http.Request) {
	if api.Manager == nil {
		writeError(w, http.StatusNotFound, ErrNotFound.Error())
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	view, err := api.Manager.Get(r.PathValue("id"), after)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (api *API) cancel(w http.ResponseWriter, _ *http.Request) {
	if api.Manager == nil || !api.Manager.Cancel() {
		writeError(w, http.StatusConflict, "nothing is running")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"cancelling": true})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// OpenAuditFile opens the permanent record of launches (see internal/auditlog). It
// returns the function that appends an entry and one that closes the file. Failure
// to open is the caller's cue to leave the launcher disabled.
func OpenAuditFile(path string) (record func(AuditEntry) error, closeFile func() error, err error) {
	log, err := auditlog.Open(path)
	if err != nil {
		return nil, nil, err
	}
	record = func(entry AuditEntry) error {
		if entry.Time.IsZero() {
			entry.Time = time.Now().UTC()
		}
		return log.Record(entry)
	}
	return record, log.Close, nil
}
