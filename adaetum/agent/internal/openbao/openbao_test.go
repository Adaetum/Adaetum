package openbao

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBao is just enough of OpenBao: Kubernetes login, and one KV v2 secret.
type fakeBao struct {
	stored   string
	sealed   bool
	roleOK   bool
	lastRole string
}

func (f *fakeBao) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.sealed {
		http.Error(w, "sealed", http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.URL.Path == "/v1/auth/kubernetes/login":
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		f.lastRole = body["role"]
		if !f.roleOK || body["jwt"] != "pod-jwt" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"auth":{"client_token":"s.token"}}`))
	case r.URL.Path == keyPath && r.Header.Get("X-Vault-Token") != "s.token":
		http.Error(w, "denied", http.StatusForbidden)
	case r.URL.Path == keyPath && r.Method == http.MethodGet:
		if f.stored == "" {
			http.Error(w, "none", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]string{"key": f.stored}}})
	case r.URL.Path == keyPath && r.Method == http.MethodPost:
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Data map[string]string `json:"data"`
		}
		json.Unmarshal(raw, &body)
		f.stored = body.Data["key"]
		w.Write([]byte(`{}`))
	default:
		http.NotFound(w, r)
	}
}

func store(t *testing.T, f *fakeBao) *KeyStore {
	t.Helper()
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	jwt := filepath.Join(t.TempDir(), "token")
	os.WriteFile(jwt, []byte("pod-jwt\n"), 0o600)
	return &KeyStore{Addr: server.URL, Role: "adaetum-agent-tls", JWTFile: jwt}
}

func TestTheKeyIsStoredAndReadBackUnderTheAgentsRole(t *testing.T) {
	f := &fakeBao{roleOK: true}
	s := store(t, f)
	if _, found, err := s.Get(t.Context()); err != nil || found {
		t.Fatalf("an empty OpenBao must say not found, not fail: found=%v err=%v", found, err)
	}
	if err := s.Put(t.Context(), []byte("-----KEY-----")); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Get(t.Context())
	if err != nil || !found || string(got) != "-----KEY-----" {
		t.Fatalf("got %q found=%v err=%v", got, found, err)
	}
	if f.lastRole != "adaetum-agent-tls" {
		t.Fatalf("signed in as %q", f.lastRole)
	}
}

func TestFailuresAreErrorsThatNeverRevealTheKeyOrTheToken(t *testing.T) {
	cases := map[string]*fakeBao{"sealed": {sealed: true}, "no role": {roleOK: false, stored: "SECRET-KEY"}}
	for name, f := range cases {
		_, _, err := store(t, f).Get(t.Context())
		if err == nil {
			t.Errorf("%s: must be an error, not an empty answer", name)
			continue
		}
		if strings.Contains(err.Error(), "SECRET-KEY") || strings.Contains(err.Error(), "pod-jwt") {
			t.Errorf("%s: the error leaks a secret: %v", name, err)
		}
	}
	missing := &KeyStore{Addr: "http://127.0.0.1:1", Role: "r", JWTFile: filepath.Join(t.TempDir(), "none")}
	if _, _, err := missing.Get(t.Context()); err == nil {
		t.Error("no service-account token must be an error")
	}
}
