package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/provider"
)

func TestEmptyTailnetDiscoveryCleansUpOnSuccessAndFailure(t *testing.T) {
	for _, failJoin := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "join failure"}[failJoin], func(t *testing.T) {
			var host string
			keyDeleted, nodeDeleted := false, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer setup-token" {
					t.Error("missing auth")
					w.WriteHeader(401)
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /tailnet/-/keys":
					var body struct {
						Capabilities struct {
							Devices struct {
								Create struct {
									Ephemeral bool `json:"ephemeral"`
									Reusable  bool `json:"reusable"`
								} `json:"create"`
							} `json:"devices"`
						} `json:"capabilities"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Capabilities.Devices.Create.Ephemeral || body.Capabilities.Devices.Create.Reusable {
						t.Error("key must be ephemeral and single-use")
					}
					w.Write([]byte(`{"id":"key-id","key":"auth-secret"}`))
				case "GET /tailnet/-/devices":
					if nodeDeleted {
						w.Write([]byte(`{"devices":[]}`))
					} else {
						json.NewEncoder(w).Encode(map[string]any{"devices": []map[string]string{{"id": "node-id", "hostname": host}}})
					}
				case "DELETE /device/node-id":
					nodeDeleted = true
					w.WriteHeader(204)
				case "DELETE /tailnet/-/keys/key-id":
					keyDeleted = true
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			join := func(_ context.Context, h, _, key string) (string, error) {
				host = h
				if key != "auth-secret" {
					t.Error("auth key not passed")
				}
				if failJoin {
					return "", errors.New("join failed")
				}
				return "tail123.ts.net.", nil
			}
			got, err := discoverEmptyTailnet(context.Background(), provider.Client{TailscaleBase: srv.URL}, "setup-token", join)
			if failJoin && err == nil {
				t.Error("expected join error")
			}
			if !failJoin && (err != nil || got != "tail123.ts.net") {
				t.Errorf("got %q %v", got, err)
			}
			if !nodeDeleted || !keyDeleted {
				t.Errorf("cleanup: node=%v key=%v", nodeDeleted, keyDeleted)
			}
		})
	}
}

func TestDiscoverySurfacesCleanupFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			w.Write([]byte(`{"id":"key-id","key":"auth-secret"}`))
		case r.Method == "GET":
			w.Write([]byte(`{"devices":[]}`))
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	_, err := discoverEmptyTailnet(context.Background(), provider.Client{TailscaleBase: srv.URL}, "setup-token", func(context.Context, string, string, string) (string, error) { return "tail123.ts.net", nil })
	if !errors.Is(err, errDiscoveryCleanup) || strings.Contains(err.Error(), "setup-token") {
		t.Fatalf("unexpected error: %v", err)
	}
}
