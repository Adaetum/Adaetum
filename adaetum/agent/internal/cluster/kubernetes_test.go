package cluster

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestKubernetesNodesReadsProjectedTokenAndTrustedCA(t *testing.T) {
	var credentials []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes" {
			t.Errorf("path = %q", r.URL.Path)
		}
		credentials = append(credentials, r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"items":[{"metadata":{"name":"node-a"}},{"metadata":{"name":"node-b"}}]}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca")
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	list := KubernetesNodes(server.URL, token, ca)
	for _, value := range []string{"first", "rotated"} {
		if err := os.WriteFile(token, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		names, err := list(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(names, []string{"node-a", "node-b"}) {
			t.Fatalf("nodes = %v", names)
		}
	}
	if !reflect.DeepEqual(credentials, []string{"Bearer first", "Bearer rotated"}) {
		t.Fatalf("authorization = %v", credentials)
	}
}

func TestKubernetesNodesRejectsFailureAndInvalidResponses(t *testing.T) {
	status := http.StatusForbidden
	body := "denied"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); fmt.Fprint(w, body) }))
	defer server.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca")
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	list := KubernetesNodes(server.URL, token, ca)
	if _, err := list(context.Background()); err == nil {
		t.Fatal("accepted forbidden response")
	}
	status = http.StatusOK
	body = `{"items":[{"metadata":{}}]}`
	if _, err := list(context.Background()); err == nil {
		t.Fatal("accepted node without name")
	}
	body = `{}`
	if _, err := list(context.Background()); err == nil {
		t.Fatal("accepted missing node list")
	}
	if err := os.WriteFile(ca, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := list(context.Background()); err == nil {
		t.Fatal("accepted invalid CA")
	}
}

func TestKubernetesNodeKeysReadsOnlyTheKeysNodesPublished(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[
			{"metadata":{"name":"Node-A.example.net","annotations":{"adaetum.io/host-key":"KEY-A"}}},
			{"metadata":{"name":"node-b","annotations":{"other":"x"}}},
			{"metadata":{"name":"node-c","annotations":{"adaetum.io/host-key":"  "}}}]}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, token := filepath.Join(dir, "ca"), filepath.Join(dir, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("t"), 0600); err != nil {
		t.Fatal(err)
	}
	keys, err := KubernetesNodeKeys(server.URL, token, ca)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Keyed by short, lower-case node name; nodes with no usable key are absent.
	if !reflect.DeepEqual(keys, map[string]string{"node-a": "KEY-A"}) {
		t.Fatalf("keys = %v", keys)
	}
}

func TestPublishEndpointPatchesThenCreatesWhenMissing(t *testing.T) {
	var calls []string
	exists := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPatch && !exists:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost:
			exists = true
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, token := filepath.Join(dir, "ca"), filepath.Join(dir, "token")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	_ = os.WriteFile(token, []byte("t"), 0600)
	for i := 0; i < 2; i++ {
		if err := PublishEndpoint(context.Background(), server.URL, token, ca, "adaetum", "https://10.43.1.2:8444", "AA:BB"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"PATCH /api/v1/namespaces/adaetum/configmaps/adaetum-cluster-agent-endpoint",
		"POST /api/v1/namespaces/adaetum/configmaps",
		"PATCH /api/v1/namespaces/adaetum/configmaps/adaetum-cluster-agent-endpoint",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestPublishEndpointReportsADeniedWrite(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	dir := t.TempDir()
	ca, token := filepath.Join(dir, "ca"), filepath.Join(dir, "token")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	_ = os.WriteFile(token, []byte("t"), 0600)
	if err := PublishEndpoint(context.Background(), server.URL, token, ca, "adaetum", "u", "f"); err == nil {
		t.Fatal("a refused write must be an error so it is retried and reported")
	}
}
