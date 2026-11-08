package releases

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// API checks releases and, only on an authenticated explicit request, proposes a
// reviewed GitOps change. It never merges or directly changes running workloads.
type API struct {
	Current     string
	Latest      func(context.Context, string, string) (Check, error)
	Propose     func(context.Context, Manifest) (Proposal, error)
	Audit       func(any) error
	Who         func(*http.Request) string
	RequireUser func(http.Handler) http.Handler
}

func (a API) Register(mux *http.ServeMux) {
	protect := func(handler http.Handler) http.Handler {
		if a.RequireUser != nil {
			return a.RequireUser(handler)
		}
		return handler
	}
	mux.Handle("GET /api/v1/releases/latest", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channel := r.URL.Query().Get("channel")
		if channel == "" {
			channel = "stable"
		}
		if channel != "stable" && channel != "beta" {
			http.Error(w, "Invalid release channel", http.StatusBadRequest)
			return
		}
		result, err := a.Latest(r.Context(), a.Current, channel)
		if err != nil {
			http.Error(w, "Could not check Adaetum releases: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})))
	mux.Handle("POST /api/v1/releases/proposals", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Propose == nil || a.Audit == nil {
			http.Error(w, "GitOps update proposals are not configured", http.StatusServiceUnavailable)
			return
		}
		var request struct {
			Version      string `json:"version"`
			Channel      string `json:"channel"`
			ManifestHash string `json:"manifestHash"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || !hashPattern.MatchString(request.ManifestHash) {
			http.Error(w, "Invalid release selection", http.StatusBadRequest)
			return
		}
		if request.Channel == "" {
			request.Channel = "stable"
		}
		if request.Channel != "stable" && request.Channel != "beta" {
			http.Error(w, "Invalid release channel", http.StatusBadRequest)
			return
		}
		latest, err := a.Latest(r.Context(), a.Current, request.Channel)
		if err != nil {
			http.Error(w, "Could not recheck the release: "+err.Error(), http.StatusBadGateway)
			return
		}
		if latest.Release.Version != request.Version || latest.ManifestHash != request.ManifestHash {
			http.Error(w, "The release changed; check it again before proposing an update", http.StatusConflict)
			return
		}
		who := "operator"
		if a.Who != nil {
			if person := a.Who(r); person != "" {
				who = person
			}
		}
		entry := map[string]any{"time": time.Now().UTC(), "event": "release_proposal_requested", "version": request.Version, "channel": request.Channel, "manifest_hash": request.ManifestHash, "by": who}
		if err := a.Audit(entry); err != nil {
			http.Error(w, "Cannot record the update proposal; nothing was changed", http.StatusServiceUnavailable)
			return
		}
		proposal, err := a.Propose(r.Context(), latest.Release)
		if err != nil {
			_ = a.Audit(map[string]any{"time": time.Now().UTC(), "event": "release_proposal_failed", "version": request.Version, "branch": proposal.Branch, "by": who})
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_ = a.Audit(map[string]any{"time": time.Now().UTC(), "event": "release_proposal_created", "version": request.Version, "branch": proposal.Branch, "by": who})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(proposal)
	})))
}
