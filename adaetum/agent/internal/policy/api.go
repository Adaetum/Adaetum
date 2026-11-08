package policy

import (
	"encoding/json"
	"net/http"
	"time"
)

// API is the HTTP surface of the switchboard. Both routes need a signed-in person:
// what is switched on is itself worth protecting.
type API struct {
	Board       *Switchboard
	RequireUser func(http.Handler) http.Handler
	// ChangedBy names the person for the record when Who gives no name: the agent has a
	// shared access token, so it is "operator".
	ChangedBy string
	// Who names the signed-in person who made the request (their email), or "" for the
	// access token. It is what lets the record say WHICH person flipped a switch.
	Who func(*http.Request) string
}

// Register adds the routes.
func (api *API) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /api/v1/security", user(http.HandlerFunc(api.list)))
	mux.Handle("PUT /api/v1/security/{key}", user(http.HandlerFunc(api.set)))
}

func (api *API) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"levers": api.Board.List()})
}

func (api *API) set(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled         bool   `json:"enabled"`
		DurationMinutes int    `json:"durationMinutes"`
		Confirm         string `json:"confirm"`
		Via             string `json:"via"`
		Reason          string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid"})
		return
	}
	by := api.ChangedBy
	if by == "" {
		by = "operator"
	}
	if api.Who != nil {
		if person := api.Who(r); person != "" {
			by = person
		}
	}
	state, err := api.Board.Set(r.PathValue("key"), SetRequest{
		Enabled: body.Enabled, Duration: time.Duration(body.DurationMinutes) * time.Minute, Confirm: body.Confirm, By: by, Via: body.Via, Reason: body.Reason,
	})
	switch {
	case err == ErrUnknownLever:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, state)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
