package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

// HTTPUpstream is the host's view of the cluster agent's account routes. Every request is
// signed with the host's own key, exactly like event delivery, so only an enrolled and
// approved host can ask for the accounts (they include password hashes).
type HTTPUpstream struct {
	Link   membership.Link
	HostID string
	Key    *identity.HostKey
	Now    func() time.Time
}

func (u *HTTPUpstream) do(ctx context.Context, method, path string, body []byte, out any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(u.Link.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	now := time.Now
	if u.Now != nil {
		now = u.Now
	}
	if err := u.Key.Sign(request, u.HostID, body, now()); err != nil {
		return 0, err
	}
	response, err := u.Link.HTTPClient().Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxReply+1))
	if err != nil {
		return response.StatusCode, err
	}
	if len(data) > maxReply {
		return response.StatusCode, fmt.Errorf("the cluster agent's answer is larger than any real account list")
	}
	if response.StatusCode/100 != 2 {
		return response.StatusCode, &StatusError{Code: response.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = data
		return response.StatusCode, nil
	}
	if out != nil {
		return response.StatusCode, json.Unmarshal(data, out)
	}
	return response.StatusCode, nil
}

func (u *HTTPUpstream) State(ctx context.Context) (State, error) {
	var state State
	_, err := u.do(ctx, http.MethodGet, "/api/v1/accounts/state", nil, &state)
	return state, err
}

func (u *HTTPUpstream) Snapshot(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	status, err := u.do(ctx, http.MethodGet, "/api/v1/accounts/snapshot", nil, &raw)
	if status == http.StatusNotFound {
		return nil, ErrNobody
	}
	return raw, err
}

func (u *HTTPUpstream) Seed(ctx context.Context, snapshot json.RawMessage) (ApplyResult, error) {
	var result ApplyResult
	status, err := u.do(ctx, http.MethodPut, "/api/v1/accounts/seed", snapshot, &result)
	if status == http.StatusConflict {
		var refused *StatusError
		if e, ok := err.(*StatusError); ok {
			refused = e
		}
		if refused != nil && json.Unmarshal([]byte(refused.Body), &result) == nil && result.Status == "rejected" {
			return result, nil
		}
	}
	return result, err
}

// HostAPI is the cluster agent's side: the routes hosts call. Each one checks the host's
// signature first. The cluster agent's own auth service is the master copy.
type HostAPI struct {
	Local *Client
	// Verify returns the host's id when the request carries a valid signature from an
	// approved host (identity.Verifier.Verify).
	Verify func(r *http.Request, body []byte) (string, error)
	// Seeded is told when a host's accounts were accepted (for the record); may be nil.
	Seeded func(hostID string, result ApplyResult)
}

const maxSeedBody = maxReply

func (a *HostAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/accounts/state", a.guard(a.state))
	mux.HandleFunc("GET /api/v1/accounts/snapshot", a.guard(a.snapshot))
	mux.HandleFunc("PUT /api/v1/accounts/seed", a.guard(a.seed))
}

type hostHandler func(w http.ResponseWriter, r *http.Request, hostID string, body []byte)

func (a *HostAPI) guard(next hostHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSeedBody))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		hostID, err := a.Verify(r, body)
		if err != nil {
			// One answer for every failure, like the event route.
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		next(w, r, hostID, body)
	}
}

func (a *HostAPI) state(w http.ResponseWriter, r *http.Request, _ string, _ []byte) {
	state, err := a.Local.State(r.Context())
	if err != nil {
		relay(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *HostAPI) snapshot(w http.ResponseWriter, r *http.Request, _ string, _ []byte) {
	snapshot, err := a.Local.Snapshot(r.Context())
	if err == ErrNobody {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		relay(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(snapshot)
}

// seed lets a host give an EMPTY cluster its accounts. The auth service refuses it when the
// cluster already has people (it owns them), so a host cannot overwrite the cluster's list.
func (a *HostAPI) seed(w http.ResponseWriter, r *http.Request, hostID string, body []byte) {
	result, err := a.Local.Apply(r.Context(), json.RawMessage(body))
	if err != nil {
		relay(w, err)
		return
	}
	if result.Status == "rejected" {
		writeJSON(w, http.StatusConflict, result)
		return
	}
	if a.Seeded != nil {
		a.Seeded(hostID, result)
	}
	writeJSON(w, http.StatusOK, result)
}
