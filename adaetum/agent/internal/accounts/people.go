package accounts

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// PeopleAPI is the page's view of accounts: who am I, who can sign in, and (for an owner)
// adding, removing and re-roling people. It never sees or returns a password hash.
type PeopleAPI struct {
	Client *Client
	// Guard is the Guard's Middleware.
	Guard func(http.Handler) http.Handler
}

const maxPersonBody = 8 << 10

func (a *PeopleAPI) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/me", a.Guard(http.HandlerFunc(a.me)))
	mux.Handle("GET /api/v1/people", a.Guard(http.HandlerFunc(a.list)))
	owner := func(h http.HandlerFunc) http.Handler { return a.Guard(OwnerOnly(h)) }
	mux.Handle("POST /api/v1/people", owner(a.add))
	mux.Handle("DELETE /api/v1/people/{id}", owner(a.remove))
	mux.Handle("POST /api/v1/people/{id}/role", owner(a.role))
	mux.Handle("POST /api/v1/people/{id}/revoke-sessions", owner(a.revoke))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// relay turns the auth service's answer into ours: its refusals (400, 404, 409) keep their
// meaning, and "could not reach it" is a 503.
func relay(w http.ResponseWriter, err error) {
	var status *StatusError
	switch {
	case errors.As(err, &status):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status.Code)
		_, _ = io.WriteString(w, status.Body)
	case Unavailable(err):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the sign-in service is not answering"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unexpected"})
	}
}

func (a *PeopleAPI) me(w http.ResponseWriter, r *http.Request) {
	who, _ := FromContext(r.Context())
	writeJSON(w, http.StatusOK, who)
}

func (a *PeopleAPI) list(w http.ResponseWriter, r *http.Request) {
	mode, people, err := a.Client.People(r.Context())
	if err != nil {
		relay(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "people": people})
}

func (a *PeopleAPI) add(w http.ResponseWriter, r *http.Request) {
	var person NewPerson
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPersonBody)).Decode(&person); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid", "message": "the form was not understood"})
		return
	}
	created, err := a.Client.AddPerson(r.Context(), person)
	if err != nil {
		relay(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *PeopleAPI) remove(w http.ResponseWriter, r *http.Request) {
	if err := a.Client.RemovePerson(r.Context(), r.PathValue("id")); err != nil {
		relay(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *PeopleAPI) role(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPersonBody)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid"})
		return
	}
	if err := a.Client.SetRole(r.Context(), r.PathValue("id"), body.Role); err != nil {
		relay(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *PeopleAPI) revoke(w http.ResponseWriter, r *http.Request) {
	if err := a.Client.RevokeSessions(r.Context(), r.PathValue("id")); err != nil {
		relay(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
