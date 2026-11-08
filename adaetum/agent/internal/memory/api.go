package memory

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// API is the HTTP surface of memory. Every route needs a signed-in person: what the
// agent remembers is as sensitive as what it can do.
type API struct {
	Service     *Service
	RequireUser func(http.Handler) http.Handler
}

// Register adds the routes.
func (api *API) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /api/v1/memory", user(http.HandlerFunc(api.list)))
	mux.Handle("POST /api/v1/memory", user(http.HandlerFunc(api.remember)))
	mux.Handle("DELETE /api/v1/memory/{id}", user(http.HandlerFunc(api.forget)))
	mux.Handle("POST /api/v1/memory/search", user(http.HandlerFunc(api.search)))
	mux.Handle("GET /api/v1/memory/status", user(http.HandlerFunc(api.status)))
	mux.Handle("POST /api/v1/memory/retry", user(http.HandlerFunc(api.retry)))
}

func (api *API) remember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text   string   `json:"text"`
		Tags   []string `json:"tags"`
		Source string   `json:"source"`
	}
	if !decode(w, r, &body) {
		return
	}
	memory, err := api.Service.Remember(r.Context(), body.Text, body.Tags, body.Source)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, memory)
}

func (api *API) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	memories, err := api.Service.List(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read memories"})
		return
	}
	if memories == nil {
		memories = []Memory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": memories})
}

func (api *API) forget(w http.ResponseWriter, r *http.Request) {
	err := api.Service.Forget(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot forget"})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"forgotten": true})
	}
}

func (api *API) search(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if !decode(w, r, &body) {
		return
	}
	results, err := api.Service.Search(r.Context(), body.Query, body.Limit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if results == nil {
		results = []Result{}
	}
	status, _ := api.Service.Status(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "degraded": status.Degraded, "embedder": status.Embedder})
}

func (api *API) status(w http.ResponseWriter, r *http.Request) {
	status, err := api.Service.Status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read status"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (api *API) retry(w http.ResponseWriter, r *http.Request) {
	n, err := api.Service.RetryFailed(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot retry"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"requeued": n})
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
