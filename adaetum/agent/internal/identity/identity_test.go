package identity

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func newKey(t *testing.T) *HostKey {
	t.Helper()
	key, err := LoadOrCreateHostKey(filepath.Join(t.TempDir(), "id"))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func verifierFor(hostID string, key *HostKey, allowed bool) *Verifier {
	return &Verifier{
		Now: func() time.Time { return now },
		PublicKey: func(id string) (string, bool) {
			if id != hostID {
				return "", false
			}
			return key.PublicKey(), allowed
		},
	}
}

func signed(t *testing.T, key *HostKey, method, path, hostID string, body []byte, at time.Time) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if err := key.Sign(request, hostID, body, at); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestTheKeyIsCreatedOncePrivatelyAndReloadsIdentically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "id")
	first, err := LoadOrCreateHostKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(dir, keyFileName))
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the private key must be readable only by its owner: %v", info.Mode())
	}
	second, err := LoadOrCreateHostKey(dir)
	if err != nil || first.PublicKey() != second.PublicKey() {
		t.Fatalf("the identity must survive a restart: %v", err)
	}
	if other := newKey(t); other.PublicKey() == first.PublicKey() {
		t.Fatal("two hosts must not share a key")
	}
}

func TestAGarbageKeyFileIsAnErrorNotASilentNewIdentity(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, keyFileName), []byte("garbage"), 0o600)
	if _, err := LoadOrCreateHostKey(dir); err == nil {
		t.Fatal("replacing a damaged key would silently change the host's identity")
	}
}

func TestAValidSignedRequestIsAccepted(t *testing.T) {
	key := newKey(t)
	body := []byte(`{"events":[]}`)
	request := signed(t, key, "POST", "/api/v1/events", "host-1", body, now)
	hostID, err := verifierFor("host-1", key, true).Verify(request, body)
	if err != nil || hostID != "host-1" {
		t.Fatalf("expected host-1: %q %v", hostID, err)
	}
}

func TestEveryWayOfTamperingIsRefused(t *testing.T) {
	key := newKey(t)
	body := []byte(`{"events":[1]}`)
	cases := map[string]func(*http.Request) (*http.Request, []byte){
		"body changed":   func(r *http.Request) (*http.Request, []byte) { return r, []byte(`{"events":[2]}`) },
		"path changed":   func(r *http.Request) (*http.Request, []byte) { r.URL.Path = "/api/v1/other"; return r, body },
		"method changed": func(r *http.Request) (*http.Request, []byte) { r.Method = "PUT"; return r, body },
		"other host":     func(r *http.Request) (*http.Request, []byte) { r.Header.Set(HeaderHost, "host-2"); return r, body },
		"no signature":   func(r *http.Request) (*http.Request, []byte) { r.Header.Del(HeaderSignature); return r, body },
		"bad signature":  func(r *http.Request) (*http.Request, []byte) { r.Header.Set(HeaderSignature, "AAAA"); return r, body },
		"nonce changed":  func(r *http.Request) (*http.Request, []byte) { r.Header.Set(HeaderNonce, "feed"); return r, body },
		"time changed":   func(r *http.Request) (*http.Request, []byte) { r.Header.Set(HeaderTimestamp, "1"); return r, body },
	}
	for name, tamper := range cases {
		request := signed(t, key, "POST", "/api/v1/events", "host-1", body, now)
		request, sent := tamper(request)
		if _, err := verifierFor("host-1", key, true).Verify(request, sent); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestAReplayedRequestIsRefused(t *testing.T) {
	key := newKey(t)
	verifier := verifierFor("host-1", key, true)
	request := signed(t, key, "POST", "/api/v1/events", "host-1", nil, now)
	if _, err := verifier.Verify(request, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(request, nil); err == nil {
		t.Fatal("a captured request must not work twice")
	}
}

func TestRequestsOutsideTheClockWindowAreRefused(t *testing.T) {
	key := newKey(t)
	for _, offset := range []time.Duration{-(MaxClockSkew + time.Minute), MaxClockSkew + time.Minute} {
		request := signed(t, key, "POST", "/x", "host-1", nil, now.Add(offset))
		if _, err := verifierFor("host-1", key, true).Verify(request, nil); err == nil {
			t.Errorf("offset %v must be refused", offset)
		}
	}
	request := signed(t, key, "POST", "/x", "host-1", nil, now.Add(-MaxClockSkew+time.Second))
	if _, err := verifierFor("host-1", key, true).Verify(request, nil); err != nil {
		t.Errorf("a modestly skewed clock must still work: %v", err)
	}
}

func TestAnUnknownOrUnapprovedHostIsRefusedEvenWithAValidSignature(t *testing.T) {
	key := newKey(t)
	request := signed(t, key, "POST", "/x", "host-1", nil, now)
	if _, err := verifierFor("host-1", key, false).Verify(request, nil); err == nil {
		t.Fatal("a pending host has proven its key but is not yet allowed to speak")
	}
	request = signed(t, key, "POST", "/x", "ghost", nil, now)
	if _, err := verifierFor("host-1", key, true).Verify(request, nil); err == nil {
		t.Fatal("an unknown host must be refused")
	}
	// A different key signing as host-1 is the impersonation case.
	attacker := newKey(t)
	request = signed(t, attacker, "POST", "/x", "host-1", nil, now)
	if _, err := verifierFor("host-1", key, true).Verify(request, nil); err == nil {
		t.Fatal("another host's key must not be accepted as host-1")
	}
}

func TestNoncesOfFailedRequestsAreNotRemembered(t *testing.T) {
	key := newKey(t)
	verifier := verifierFor("host-1", key, true)
	attacker := newKey(t)
	for i := 0; i < 50; i++ {
		verifier.Verify(signed(t, attacker, "POST", "/x", "host-1", nil, now), nil)
	}
	if len(verifier.nonces) != 0 {
		t.Fatalf("unauthenticated traffic must not grow the replay cache: %d", len(verifier.nonces))
	}
}

func TestJoinTokensAreLongRandomAndStoredOnlyAsAHash(t *testing.T) {
	first, _ := NewJoinToken(now, time.Hour)
	second, _ := NewJoinToken(now, time.Hour)
	if first.Secret == second.Secret || !strings.HasPrefix(first.Secret, "adj_") || len(first.Secret) < 64 {
		t.Fatalf("tokens must be unique and long: %q", first.Secret)
	}
	if first.Hash == first.Secret || !SameHash(first.Hash, HashToken(first.Secret)) {
		t.Fatal("the stored value must be a hash that the secret reproduces")
	}
	if SameHash(first.Hash, second.Hash) {
		t.Fatal("different tokens must hash differently")
	}
	if !first.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected expiry: %v", first.ExpiresAt)
	}
}

func TestTrustDecisions(t *testing.T) {
	nodes := []string{"node-50449", "node-56843"}
	cases := []struct {
		name       string
		hostname   string
		nodes      []string
		known      bool
		tokenValid bool
		want       Trust
	}{
		{"known node with a token", "node-50449", nodes, true, true, Approved},
		{"fully qualified name of a known node", "NODE-56843.tailnet.ts.net", nodes, true, true, Approved},
		{"new host with a token waits for a person", "new-box", nodes, true, true, Pending},
		{"a known name without a token is rejected", "node-50449", nodes, true, false, Rejected},
		{"node list unavailable asks a person", "node-50449", nil, false, true, Pending},
		{"empty hostname is never a match", "", []string{""}, true, true, Pending},
	}
	for _, c := range cases {
		got, reason := Decide(c.hostname, c.nodes, c.known, c.tokenValid)
		if got != c.want || reason == "" {
			t.Errorf("%s: want %s, got %s (%q)", c.name, c.want, got, reason)
		}
	}
}
