package backups

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// StorageAPI serves POST /api/v1/backups/storage: choose a tier's storage. Every attempt is
// recorded before anything is created: a bucket or key nobody can later trace is not made.
type StorageAPI struct {
	Enabler     *Enabler
	Audit       func(entry any) error
	RequireUser func(http.Handler) http.Handler
	Who         func(*http.Request) string
}

// Register adds the route behind the sign-in guard.
func (a *StorageAPI) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(a.serve)
	if a.RequireUser != nil {
		handler = a.RequireUser(handler)
	}
	mux.Handle("POST /api/v1/backups/storage", handler)
}

func (a *StorageAPI) serve(w http.ResponseWriter, r *http.Request) {
	var request Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil {
		reply(w, http.StatusBadRequest, map[string]any{"error": "unreadable request"})
		return
	}
	if a.Audit == nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because there is no record to write them to."})
		return
	}
	who := ""
	if a.Who != nil {
		who = a.Who(r)
	}
	// The record names the provider and bucket, never a key or token.
	if err := a.Audit(map[string]any{"time": time.Now().UTC(), "action": "choose-storage", "tier": request.Tier, "provider": request.Provider, "same": request.Same, "bucket": request.target(), "by": who}); err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "Changes are refused because the record cannot be written."})
		return
	}
	result, err := a.Enabler.Configure(r.Context(), request)
	var choice *AccountChoiceError
	switch {
	case errors.As(err, &choice):
		reply(w, http.StatusConflict, map[string]any{"error": err.Error(), "accounts": choice.Accounts})
	case err != nil:
		reply(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "bucket": result.Bucket})
	default:
		reply(w, http.StatusOK, result)
	}
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
