package memory

import (
	"errors"
	"net/http"
)

// EmbeddingAPI is the management surface for where embeddings come from: what is in
// use, a test of a candidate, a change, and re-embedding after a change. Everything
// needs a signed-in person, and sending memories to an outside service additionally
// needs an explicit confirmation.
type EmbeddingAPI struct {
	Manager     *Manager
	Service     *Service
	RequireUser func(http.Handler) http.Handler
	// KeyAvailable says whether the agent was started with an API key for external
	// services, so the page can offer or hide that choice.
	KeyAvailable bool
}

// Register adds the routes.
func (api *EmbeddingAPI) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /api/v1/embedding", user(http.HandlerFunc(api.get)))
	mux.Handle("POST /api/v1/embedding/test", user(http.HandlerFunc(api.test)))
	mux.Handle("PUT /api/v1/embedding", user(http.HandlerFunc(api.set)))
	mux.Handle("POST /api/v1/embedding/reembed", user(http.HandlerFunc(api.reembed)))
}

func (api *EmbeddingAPI) get(w http.ResponseWriter, r *http.Request) {
	status, err := api.Service.Status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read the status"})
		return
	}
	models, _ := api.Service.Models(r.Context())
	current := api.Manager.Model()
	stale := 0
	for model, n := range models {
		if model != current {
			stale += n
		}
	}
	cfg := api.Manager.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"config":         cfg,
		"model":          current,
		"enabled":        api.Manager.Enabled(),
		"external":       IsExternal(cfg),
		"status":         status,
		"vectorsByModel": models,
		// Vectors made by a different model than the one in use: memories that look
		// searchable by meaning but are not, until they are re-embedded.
		"staleVectors":   stale,
		"keyAvailable":   api.KeyAvailable,
		"defaultService": DefaultServiceURL,
	})
}

func (api *EmbeddingAPI) test(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if !decode(w, r, &cfg) {
		return
	}
	writeJSON(w, http.StatusOK, api.Manager.Test(r.Context(), cfg))
}

func (api *EmbeddingAPI) set(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Config
		ConfirmExternal bool `json:"confirmExternal"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := api.Manager.Set(r.Context(), body.Config, body.ConfirmExternal)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "test": result})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "test": result, "model": api.Manager.Model()})
}

func (api *EmbeddingAPI) reembed(w http.ResponseWriter, r *http.Request) {
	n, err := api.Service.RequeueAll(r.Context())
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errDisabled) || err.Error() == "embedding is switched off" {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"queued": n})
}

var errDisabled = errors.New("embedding is switched off")
