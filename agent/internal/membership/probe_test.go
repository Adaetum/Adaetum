package membership

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeSaysWhetherThePinnedClusterAgentAnswers(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") }))
	defer server.Close()
	sum := sha256.Sum256(server.Certificate().Raw)

	good := Link{URL: server.URL, Fingerprint: hex.EncodeToString(sum[:])}
	if err := good.Probe(context.Background()); err != nil {
		t.Fatalf("a reachable cluster agent with the pinned certificate must pass: %v", err)
	}
	wrong := Link{URL: server.URL, Fingerprint: "00" + hex.EncodeToString(make([]byte, 31))}
	if wrong.Probe(context.Background()) == nil {
		t.Fatal("a server with a different certificate must count as unreachable")
	}
	server.Close()
	if good.Probe(context.Background()) == nil {
		t.Fatal("a stopped cluster agent must count as unreachable")
	}
}
