package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Status says whether the assistant is available, and if not why. It is what the
// page reads first.
type Status struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	Model   string `json:"model,omitempty"`
	// Provider and Endpoint say where conversations are sent, so the page can tell
	// people before they type: "Anthropic", or the host of an OpenAI-compatible service.
	Provider string `json:"provider,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// API is the HTTP surface of the assistant.
type API struct {
	// Service is nil when the assistant is off.
	Service *Service
	Status  Status
	// Manager, when set, owns the AI service choice: the status is read from it on
	// every request, and the settings routes exist. Without one (tests) Status is fixed.
	Manager     *Manager
	RequireUser func(http.Handler) http.Handler
}

func (api *API) status() Status {
	if api.Manager != nil {
		return api.Manager.Status()
	}
	return api.Status
}

// OffReason is the standing explanation when the assistant is not enabled.
const OffReason = "The assistant is off. It sends your conversation and what it reads from the agent to an external AI service, so it only runs once someone picks the service and switches it on (AI model page), or an operator starts the agent with -enable-assistant."

// Register adds the routes. Only the status is open; a conversation needs a login.
func (api *API) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	mux.HandleFunc("GET /api/v1/assistant/status", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, api.status()) })
	if api.Manager != nil {
		mux.Handle("GET /api/v1/assistant/settings", user(http.HandlerFunc(api.getSettings)))
		mux.Handle("POST /api/v1/assistant/settings/test", user(http.HandlerFunc(api.testSettings)))
		mux.Handle("PUT /api/v1/assistant/settings", user(http.HandlerFunc(api.putSettings)))
		mux.Handle("DELETE /api/v1/assistant/settings/key", user(http.HandlerFunc(api.forgetKey)))
	}
	mux.Handle("POST /api/v1/assistant/conversations", user(http.HandlerFunc(api.create)))
	mux.Handle("GET /api/v1/assistant/conversations/{id}", user(http.HandlerFunc(api.view)))
	mux.Handle("POST /api/v1/assistant/conversations/{id}/messages", user(http.HandlerFunc(api.send)))
	mux.Handle("POST /api/v1/assistant/conversations/{id}/approvals/{tool}", user(http.HandlerFunc(api.decide)))
	mux.Handle("POST /api/v1/assistant/conversations/{id}/cancel", user(http.HandlerFunc(api.cancel)))
}

func (api *API) ready(w http.ResponseWriter) bool {
	if status := api.status(); api.Service == nil || !status.Enabled {
		reason := status.Reason
		if reason == "" {
			reason = OffReason
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return false
	}
	return true
}

// settingsBody is a settings change. The key is only ever received, never sent back.
type settingsBody struct {
	Settings
	APIKey          string `json:"apiKey,omitempty"`
	ConfirmExternal bool   `json:"confirmExternal,omitempty"`
}

func (api *API) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":  api.Manager.Settings(),
		"status":    api.Manager.Status(),
		"keySource": api.Manager.KeySource(), // "", "entered" or "startup": never the key
		"presets":   Presets(),
	})
}

func (api *API) testSettings(w http.ResponseWriter, r *http.Request) {
	var body settingsBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid"})
		return
	}
	writeJSON(w, http.StatusOK, api.Manager.Test(r.Context(), body.Settings, body.APIKey))
}

func (api *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settingsBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid"})
		return
	}
	result, err := api.Manager.Set(r.Context(), body.Settings, body.APIKey, body.ConfirmExternal)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "test": result})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "test": result, "status": api.Manager.Status()})
}

func (api *API) forgetKey(w http.ResponseWriter, _ *http.Request) {
	if err := api.Manager.ForgetKey(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keySource": api.Manager.KeySource()})
}

func (api *API) create(w http.ResponseWriter, _ *http.Request) {
	if !api.ready(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": api.Service.NewConversation()})
}

func (api *API) view(w http.ResponseWriter, r *http.Request) {
	if !api.ready(w) {
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	view, err := api.Service.View(r.PathValue("id"), after)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (api *API) send(w http.ResponseWriter, r *http.Request) {
	if !api.ready(w) {
		return
	}
	var body struct {
		Text string `json:"text"`
		// Page is what the person is looking at, attached by the web page when they
		// have it switched on. Optional.
		Page string `json:"page"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid"})
		return
	}
	switch err := api.Service.SendInContext(r.PathValue("id"), body.Text, body.Page); {
	case errors.Is(err, ErrNoConversation):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrBusy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
	}
}

func (api *API) decide(w http.ResponseWriter, r *http.Request) {
	if !api.ready(w) {
		return
	}
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid"})
		return
	}
	switch err := api.Service.Decide(r.PathValue("id"), r.PathValue("tool"), body.Approve); {
	case errors.Is(err, ErrNoConversation), errors.Is(err, ErrNothingToDecide):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot record the decision"})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"decided": true})
	}
}

func (api *API) cancel(w http.ResponseWriter, r *http.Request) {
	if !api.ready(w) {
		return
	}
	if err := api.Service.Cancel(r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"cancelling": true})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
