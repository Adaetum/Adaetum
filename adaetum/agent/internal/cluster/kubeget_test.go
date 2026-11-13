package cluster

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kubeFake is an API server that records what it was asked and answers each path.
func kubeFake(t *testing.T, answers map[string]string) (*KubeReader, *[]string) {
	t.Helper()
	var asked []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer sa-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := answers[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	token, ca := filepath.Join(dir, "token"), filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(token, []byte("sa-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return NewKubeReader(server.URL, token, ca), &asked
}

func TestGetMergesKindsAndLabelsEachItem(t *testing.T) {
	reader, asked := kubeFake(t, map[string]string{
		"/apis/apps/v1/deployments": `{"items":[{"metadata":{"name":"web"}}]}`,
		"/apis/apps/v1/daemonsets":  `{"items":[{"metadata":{"name":"agent"}}]}`,
	})
	out, err := reader.Get(context.Background(), "deployments,daemonsets", "-A")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []struct {
			Kind     string                `json:"kind"`
			Metadata struct{ Name string } `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[0].Kind != "Deployment" || list.Items[1].Kind != "DaemonSet" {
		t.Fatalf("items must carry the kind kubectl would give them: %+v", list.Items)
	}
	for _, request := range *asked {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatalf("every request must be a read: %s", request)
		}
	}
}

func TestGetScopesByNamespaceSelectorAndName(t *testing.T) {
	reader, asked := kubeFake(t, map[string]string{
		"/api/v1/events": `{"items":[]}`,
		"/apis/longhorn.io/v1beta2/namespaces/longhorn-system/settings/over-provisioning": `{"kind":"Setting","value":"200"}`,
	})
	if _, err := reader.Get(context.Background(), "events", "-A", "--field-selector", "type=Warning"); err != nil {
		t.Fatal(err)
	}
	if got := (*asked)[0]; got != "GET /api/v1/events?fieldSelector=type%3DWarning" {
		t.Fatalf("selector not passed through: %s", got)
	}
	out, err := reader.Get(context.Background(), "settings.longhorn.io", "over-provisioning", "-n", "longhorn-system")
	if err != nil || !strings.Contains(string(out), `"value":"200"`) {
		t.Fatalf("a named read returns the object itself: %s %v", out, err)
	}
}

func TestGetRefusesWhatItDoesNotKnow(t *testing.T) {
	reader, asked := kubeFake(t, nil)
	for _, c := range [][]string{{"secrets", "-A"}, {"pods", "-o", "yaml"}, {"pods,services", "one"}} {
		if _, err := reader.Get(context.Background(), c[0], c[1:]...); err == nil {
			t.Fatalf("%v must be refused", c)
		}
	}
	if len(*asked) != 0 {
		t.Fatalf("a refused read must not reach the API server: %v", *asked)
	}
}

func TestGetReportsAPIErrorsAndRawReadsAPath(t *testing.T) {
	reader, _ := kubeFake(t, map[string]string{"/apis/metrics.k8s.io/v1beta1/nodes": `{"items":[]}`})
	if _, err := reader.Get(context.Background(), "pods", "-A"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("a missing resource must say so: %v", err)
	}
	if out, err := reader.Raw(context.Background(), "/apis/metrics.k8s.io/v1beta1/nodes"); err != nil || string(out) != `{"items":[]}` {
		t.Fatalf("raw: %s %v", out, err)
	}
	if _, err := reader.Raw(context.Background(), "nodes"); err == nil {
		t.Fatal("a raw read needs an absolute API path")
	}
}
