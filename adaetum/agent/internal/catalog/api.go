package catalog

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// API serves the explorer. Both routes only read public sources and change nothing,
// but they make the agent open outside connections on a caller's behalf, so they
// need a login like every other route that is not plain status.
type API struct {
	Client      *Client
	RequireUser func(http.Handler) http.Handler
	// DB, when set, keeps logo lookups across restarts (see Migrations). Optional.
	DB *sql.DB

	logos logoCache
}

func (api *API) Register(mux *http.ServeMux) {
	user := api.RequireUser
	if user == nil {
		user = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /api/v1/catalog/search", user(http.HandlerFunc(api.search)))
	mux.Handle("GET /api/v1/catalog/preview", user(http.HandlerFunc(api.preview)))
	mux.Handle("GET /api/v1/catalog/logo", user(http.HandlerFunc(api.logo)))
}

func (api *API) client() *Client {
	if api.Client != nil {
		return api.Client
	}
	return &Client{}
}

func (api *API) search(w http.ResponseWriter, r *http.Request) {
	results, err := api.client().Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (api *API) preview(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	preview, err := api.client().Preview(r.Context(), PreviewRequest{
		Kind: Kind(query.Get("kind")), ID: query.Get("id"),
		Name: query.Get("name"), Namespace: query.Get("namespace"), Tag: query.Get("tag"),
	})
	if err != nil {
		// The messages are written for people ("Docker Hub has no tag ...").
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
