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
