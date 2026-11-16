package tailnet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const ingresses = `{"items":[
 {"spec":{"ingressClassName":"tailscale"},"status":{"loadBalancer":{"ingress":[{"hostname":"a.ts.net"}]}}},
 {"spec":{"ingressClassName":"tailscale"},"status":{"loadBalancer":{}}},
 {"spec":{"ingressClassName":"nginx"},"status":{"loadBalancer":{}}}]}`

func TestStateReadsOnlyTailscaleIngresses(t *testing.T) {
	state, err := stateFromIngresses([]byte(ingresses))
	if err != nil || state.State != "off" || state.Total != 2 || state.Waiting != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if state, _ := stateFromIngresses([]byte(`{"items":[]}`)); state.State != "unknown" {
		t.Fatalf("no Tailscale Ingress means unknown, got %q", state.State)
	}
}

// fakeTailscale remembers the setting the way the real API does.
func fakeTailscale(t *testing.T, enabled *bool, wantToken string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantToken {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPatch {
			var body struct{ HTTPSEnabled bool }
			_ = json.NewDecoder(r.Body).Decode(&body)
			*enabled = body.HTTPSEnabled
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"httpsEnabled": *enabled})
	}))
}

func post(t *testing.T, api *API, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	api.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tailnet/https", strings.NewReader(body)))
	return rec
}

func TestTurningItOnRecordsFirstThenChangesAndReadsBack(t *testing.T) {
	enabled := false
	server := fakeTailscale(t, &enabled, "tskey-secret")
	defer server.Close()
	var recorded []any
	api := &API{Base: server.URL, Audit: func(e any) error { recorded = append(recorded, e); return nil },
		Ingresses: func(context.Context) ([]byte, error) { return []byte(ingresses), nil }}
	rec := post(t, api, `{"enabled":true,"token":"tskey-secret"}`)
	if rec.Code != 200 || !enabled || len(recorded) != 1 {
		t.Fatalf("code=%d enabled=%v recorded=%d body=%s", rec.Code, enabled, len(recorded), rec.Body)
	}
}

func TestNothingChangesWithoutARecordOrAToken(t *testing.T) {
	enabled := false
	server := fakeTailscale(t, &enabled, "tskey-secret")
	defer server.Close()
	noRecord := &API{Base: server.URL, Audit: func(any) error { return http.ErrAbortHandler }}
	if rec := post(t, noRecord, `{"enabled":true,"token":"tskey-secret"}`); rec.Code != http.StatusServiceUnavailable || enabled {
		t.Fatalf("an unwritable record must stop the change: %d enabled=%v", rec.Code, enabled)
	}
	api := &API{Base: server.URL, Audit: func(any) error { return nil }}
	if rec := post(t, api, `{"enabled":true,"token":" "}`); rec.Code != http.StatusBadRequest || enabled {
		t.Fatalf("a blank token must be refused: %d", rec.Code)
	}
}

func TestAWrongTokenIsExplainedAndNeverEchoed(t *testing.T) {
	enabled := false
	server := fakeTailscale(t, &enabled, "tskey-secret")
	defer server.Close()
	api := &API{Base: server.URL, Audit: func(any) error { return nil }}
	rec := post(t, api, `{"enabled":true,"token":"tskey-wrong"}`)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "tskey-wrong") || !strings.Contains(rec.Body.String(), "refused") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}
