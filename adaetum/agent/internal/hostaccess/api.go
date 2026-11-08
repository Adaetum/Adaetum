package hostaccess

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Capabilities says whether the override is on, and if not why. Off is the normal
// state and the message says how to turn it on deliberately.
type Capabilities struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// OffReason is the standing explanation shown when the override is not enabled.
const OffReason = "Running commands directly on servers is off. The agent changes servers only by launching Ansible playbooks. " +
	"An operator can turn it on in Security settings, or start the cluster agent with -allow-host-ssh."

// API is the HTTP surface of the override.
type API struct {
	// Manager is nil when the override is off.
	Manager      *Manager
	Capabilities Capabilities
	RequireUser  func(http.Handler) http.Handler
	// Enabled, when set, is asked on every request whether the override is switched
	// on right now (the Security page's switch).
	Enabled func() (bool, string)
}

// Register adds the routes. Running a command needs a signed-in person; the
// capabilities only say whether the feature exists.
func (api *API) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	mux.HandleFunc("GET /api/v1/host-access/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		capabilities := api.Capabilities
		if on, reason := api.switchedOn(); !on {
			capabilities.Enabled, capabilities.Reason = false, reason
		}
		writeJSON(w, http.StatusOK, capabilities)
	})
	mux.Handle("POST /api/v1/host-access/commands", user(http.HandlerFunc(api.run)))
}

func (api *API) switchedOn() (bool, string) {
	if api.Manager == nil {
		return false, api.Capabilities.Reason
	}
	if api.Enabled != nil {
		return api.Enabled()
	}
	return true, ""
}

func (api *API) run(w http.ResponseWriter, r *http.Request) {
	if on, reason := api.switchedOn(); !on {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return
	}
	var request Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid"})
		return
	}
	result, err := api.Manager.Run(r.Context(), request, "operator")
	switch {
	case errors.Is(err, ErrBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil && result.DurationMs == 0 && result.Output == "":
		// Refused before anything ran (bad request, not an allowed server, not recorded).
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "result": result})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
