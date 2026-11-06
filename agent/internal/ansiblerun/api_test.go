package ansiblerun

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func apiServer(t *testing.T, enabled bool) (*httptest.Server, *Manager) {
	t.Helper()
	api := &API{
		Capabilities: Capabilities{Enabled: enabled, Reason: "Launching playbooks is switched off.", Controller: "node-a"},
		RequireUser: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer good" {
					http.Error(w, "login", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
			})
		},
	}
	var manager *Manager
	if enabled {
		manager = newManager(&fakeExec{}, &auditLog{})
		api.Manager = manager
	}
	mux := http.NewServeMux()
	api.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, manager
}

func call(t *testing.T, method, url string, body any, token string) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, _ := http.NewRequest(method, url, reader)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

func TestCapabilitiesDescribeTheCatalogWithoutALogin(t *testing.T) {
	server, _ := apiServer(t, true)
	status, body := call(t, "GET", server.URL+"/api/v1/ansible/capabilities", nil, "")
	catalog, _ := body["catalog"].([]any)
	if status != 200 || body["enabled"] != true || len(catalog) != len(Catalog()) {
		t.Fatalf("%d %v", status, body)
	}
}

func TestStartingReadingAndCancellingNeedALogin(t *testing.T) {
	server, _ := apiServer(t, true)
	for _, c := range [][2]string{{"GET", "/api/v1/ansible/runs"}, {"POST", "/api/v1/ansible/runs"}, {"GET", "/api/v1/ansible/runs/abc"}, {"POST", "/api/v1/ansible/runs/abc/cancel"}} {
		if status, _ := call(t, c[0], server.URL+c[1], nil, ""); status != http.StatusUnauthorized {
			t.Errorf("%s %s must need a login: %d", c[0], c[1], status)
		}
	}
}

func TestADisabledLauncherRefusesToStartAndSaysWhy(t *testing.T) {
	server, _ := apiServer(t, false)
	status, body := call(t, "POST", server.URL+"/api/v1/ansible/runs", goodRequest, "good")
	if status != http.StatusForbidden || !strings.Contains(body["error"].(string), "switched off") {
		t.Fatalf("%d %v", status, body)
	}
	if status, body := call(t, "GET", server.URL+"/api/v1/ansible/runs", nil, "good"); status != 200 || len(body["runs"].([]any)) != 0 {
		t.Fatalf("a disabled launcher has no runs: %d %v", status, body)
	}
}

func TestALaunchRunsAndItsOutputCanBeReadIncrementally(t *testing.T) {
	server, manager := apiServer(t, true)
	status, body := call(t, "POST", server.URL+"/api/v1/ansible/runs", goodRequest, "good")
	if status != http.StatusAccepted {
		t.Fatalf("%d %v", status, body)
	}
	manager.Wait()
	id := body["id"].(string)

	status, view := call(t, "GET", server.URL+"/api/v1/ansible/runs/"+id, nil, "good")
	if status != 200 || view["status"] != "succeeded" || len(view["lines"].([]any)) != 2 {
		t.Fatalf("%d %v", status, view)
	}
	_, later := call(t, "GET", server.URL+"/api/v1/ansible/runs/"+id+"?after=2", nil, "good")
	if len(later["lines"].([]any)) != 0 {
		t.Fatalf("polling past the end must return nothing: %v", later)
	}
	_, list := call(t, "GET", server.URL+"/api/v1/ansible/runs", nil, "good")
	if len(list["runs"].([]any)) != 1 {
		t.Fatalf("%v", list)
	}
}

func TestBadRequestsGetClearErrors(t *testing.T) {
	server, _ := apiServer(t, true)
	cases := map[string]Request{
		"unknown playbook": {Playbook: "rm-rf", Limit: "a"},
		"all servers":      {Playbook: "host-agent", Limit: "all"},
		"no servers":       {Playbook: "host-agent"},
		"undeclared var":   {Playbook: "host-agent", Limit: "a", Vars: map[string]string{"ansible_become_pass": "x"}},
	}
	for name, request := range cases {
		status, body := call(t, "POST", server.URL+"/api/v1/ansible/runs", request, "good")
		if status != http.StatusBadRequest || body["error"] == "" {
			t.Errorf("%s: %d %v", name, status, body)
		}
	}
}

func TestOnlyOneLaunchAtATimeIs409(t *testing.T) {
	api := &API{Capabilities: Capabilities{Enabled: true}}
	f := &fakeExec{gate: make(chan struct{})}
	api.Manager = newManager(f, &auditLog{})
	mux := http.NewServeMux()
	api.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	defer close(f.gate)

	if status, _ := call(t, "POST", server.URL+"/api/v1/ansible/runs", goodRequest, ""); status != http.StatusAccepted {
		t.Fatal("first launch must be accepted")
	}
	if status, _ := call(t, "POST", server.URL+"/api/v1/ansible/runs", goodRequest, ""); status != http.StatusConflict {
		t.Fatalf("a second launch must be 409: %d", status)
	}
	if status, _ := call(t, "POST", server.URL+"/api/v1/ansible/runs/x/cancel", nil, ""); status != http.StatusAccepted {
		t.Fatal("cancel of the running one must be accepted")
	}
	api.Manager.Wait()
}

func TestTheAuditFileIsPrivateAppendOnlyJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "ansible-runs.log")
	record, closeFile, err := OpenAuditFile(path)
	if err != nil {
		t.Fatal(err)
	}
	record(AuditEntry{Event: "start", ID: "1", Playbook: "host-agent"})
	record(AuditEntry{Event: "finish", ID: "1", Playbook: "host-agent", Status: StatusSucceeded})
	closeFile()
	// Reopening must append, not truncate.
	record2, close2, _ := OpenAuditFile(path)
	record2(AuditEntry{Event: "start", ID: "2", Playbook: "image-cleanup"})
	close2()

	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the record must be private: %v", info.Mode())
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three records: %q", data)
	}
	for _, line := range lines {
		var entry AuditEntry
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Time.IsZero() {
			t.Fatalf("each line must be a complete JSON record with a time: %q", line)
		}
	}
}
