// Package tailnet is the Settings page's switch for Tailscale HTTPS certificates.
//
// Every private link in the cluster (agent, Argo CD, Rancher, Gitea, ...) is a Tailscale
// Ingress, and the Tailscale operator will not give one an address until the TAILNET has
// HTTPS certificates enabled. That is a tailnet-wide setting in the operator's Tailscale
// account, not something in the cluster, so changing it means calling the Tailscale API.
//
// WHY THE TOKEN IS TYPED EACH TIME. Nothing in the cluster holds a credential that may
// change tailnet settings, and this package does not add one: a standing token here would
// let a compromised pod rewrite the tailnet. The person flipping the switch pastes a
// Tailscale access token (or an OAuth client's) that may change settings; it is used for
// exactly one request, never stored, never logged, and scrubbed from any error.
//
// WHAT THE STATE IS READ FROM. The switch's current position is read from the cluster, with
// no credential: whether every Tailscale-class Ingress has been given an address. The
// Tailscale API is only called to change the setting, then to read it back, so success is
// reported only when Tailscale itself shows the new value.
//
// The installer asks the same question once at first run (installer/provider); this is the
// way to change the answer afterwards.
package tailnet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBase = "https://api.tailscale.com/api/v2"

// State is what the cluster shows about the tailnet's HTTPS setting.
type State struct {
	// State is "on" (every Tailscale Ingress has an address), "off" (at least one is still
	// waiting for one, which is what a tailnet without HTTPS looks like), or "unknown"
	// (there are no Tailscale Ingresses to judge by).
	State   string `json:"state"`
	Waiting int    `json:"waiting"`
	Total   int    `json:"total"`
}

// API serves GET and POST /api/v1/tailnet/https.
type API struct {
	// Base is the Tailscale API root; empty means the real one. Tests point it elsewhere.
	Base string
	// Ingresses returns `kubectl get ingresses -A -o json`.
	Ingresses func(ctx context.Context) ([]byte, error)
	// Audit records a change BEFORE it is made; if it fails, nothing is changed.
	Audit       func(entry any) error
	RequireUser func(http.Handler) http.Handler
	Who         func(*http.Request) string
	HTTP        *http.Client
}

// Register adds the routes, behind the sign-in guard when there is one.
func (a *API) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(a.serve)
	if a.RequireUser != nil {
		handler = a.RequireUser(handler)
	}
	mux.Handle("/api/v1/tailnet/https", handler)
}

func (a *API) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		state, err := a.State(r.Context())
		if err != nil {
			fail(w, http.StatusBadGateway, "Could not read the cluster's Ingresses: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, state)
	case http.MethodPost:
		a.change(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		fail(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

// State reads the setting's position from the cluster.
func (a *API) State(ctx context.Context) (State, error) {
	if a.Ingresses == nil {
		return State{State: "unknown"}, nil
	}
	data, err := a.Ingresses(ctx)
	if err != nil {
		return State{}, err
	}
	return stateFromIngresses(data)
}

func stateFromIngresses(data []byte) (State, error) {
	var list struct {
		Items []struct {
			Spec struct {
				IngressClassName string `json:"ingressClassName"`
			} `json:"spec"`
			Status struct {
				LoadBalancer struct {
					Ingress []json.RawMessage `json:"ingress"`
				} `json:"loadBalancer"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return State{}, fmt.Errorf("unreadable Ingress list: %w", err)
	}
	var state State
	for _, item := range list.Items {
		if item.Spec.IngressClassName != "tailscale" {
			continue
		}
		state.Total++
		if len(item.Status.LoadBalancer.Ingress) == 0 {
			state.Waiting++
		}
	}
	switch {
	case state.Total == 0:
		state.State = "unknown"
	case state.Waiting > 0:
		state.State = "off"
	default:
		state.State = "on"
	}
	return state, nil
}

type request struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
}

func (a *API) change(w http.ResponseWriter, r *http.Request) {
	var body request
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "unreadable request")
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		fail(w, http.StatusBadRequest, "Paste a Tailscale access token that may change tailnet settings. It is used once and not kept.")
		return
	}
	// The record comes first: a change nobody can later see is not made.
	if a.Audit == nil {
		fail(w, http.StatusServiceUnavailable, "Changes are refused because there is no record to write them to.")
		return
	}
	who := ""
	if a.Who != nil {
		who = a.Who(r)
	}
	if err := a.Audit(map[string]any{"time": time.Now().UTC(), "setting": "tailnet-https", "enabled": body.Enabled, "by": who}); err != nil {
		fail(w, http.StatusServiceUnavailable, "Changes are refused because the record cannot be written.")
		return
	}
	if err := a.apply(r.Context(), token, body.Enabled); err != nil {
		fail(w, http.StatusBadGateway, strings.ReplaceAll(err.Error(), token, "[token]"))
		return
	}
	// The operator notices the change on its own schedule, so the page keeps asking.
	state, _ := a.State(r.Context())
	writeJSON(w, http.StatusOK, state)
}

// apply sets the tailnet setting and reads it back, so success means Tailscale shows it.
func (a *API) apply(ctx context.Context, token string, enabled bool) error {
	if err := a.settings(ctx, token, http.MethodPatch, fmt.Sprintf(`{"httpsEnabled":%t}`, enabled), nil); err != nil {
		return err
	}
	var now struct {
		HTTPSEnabled *bool `json:"httpsEnabled"`
	}
	if err := a.settings(ctx, token, http.MethodGet, "", &now); err != nil {
		return err
	}
	if now.HTTPSEnabled == nil || *now.HTTPSEnabled != enabled {
		return errors.New("Tailscale accepted the change but does not show it applied")
	}
	return nil
}

func (a *API) settings(ctx context.Context, token, method, payload string, into any) error {
	base := a.Base
	if base == "" {
		base = defaultBase
	}
	var reader io.Reader
	if payload != "" {
		reader = strings.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+"/tailnet/-/settings", reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "adaetum-agent")
	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Tailscale: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("Tailscale refused that token. It needs permission to change tailnet settings (an admin's access token, or an OAuth client with the feature settings scope)")
	case resp.StatusCode >= 400:
		return fmt.Errorf("Tailscale answered HTTP %d", resp.StatusCode)
	}
	if into != nil {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err := json.NewDecoder(bytes.NewReader(raw)).Decode(into); err != nil {
			return errors.New("Tailscale's answer was unreadable")
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
