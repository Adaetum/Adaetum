package ansiblerun

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKubernetesJobUsesRunnerAndPrivateVariables(t *testing.T) {
	var mu sync.Mutex
	var dry, created, secret, deletedJob, deletedSecret bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer service-token" {
			t.Errorf("bad Kubernetes credential")
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/deployments/ansible-runner"):
			fmt.Fprint(w, `{"spec":{"template":{"spec":{"serviceAccountName":"ansible-runner","hostNetwork":true,"containers":[{"name":"ansible-runner","image":"registry.example/runner@sha256:abc","volumeMounts":[{"name":"ansible-data","mountPath":"/ansible"}]}],"volumes":[{"name":"ansible-data","hostPath":{"path":"/opt/ansible-runner","type":"Directory"}}]}}}}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pods") && r.URL.Query().Get("labelSelector") == "app=ansible-runner":
			fmt.Fprint(w, `{"items":[{"spec":{"nodeName":"node-a"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}]}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/jobs"):
			var job map[string]any
			if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			raw, _ := json.Marshal(job)
			if bytes.Contains(raw, []byte("join-secret")) {
				t.Error("secret was put in the Job")
			}
			spec := job["spec"].(map[string]any)
			pod := spec["template"].(map[string]any)["spec"].(map[string]any)
			if pod["nodeName"] != "node-a" || pod["serviceAccountName"] != "ansible-runner" {
				t.Errorf("wrong runner pod: %v", pod)
			}
			container := pod["containers"].([]any)[0].(map[string]any)
			args := container["args"].([]any)
			if fmt.Sprint(args) != "[playbooks/host-agent.yml --limit node-a -e @/run/adaetum-job/vars.json --check --diff]" {
				t.Errorf("wrong command args: %v", args)
			}
			if r.URL.Query().Get("dryRun") == "All" {
				dry = true
				fmt.Fprint(w, `{}`)
			} else {
				created = true
				fmt.Fprint(w, `{"metadata":{"uid":"job-uid"}}`)
			}
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/secrets"):
			var s struct {
				Data     map[string]string `json:"data"`
				Metadata struct {
					OwnerReferences []struct {
						UID string `json:"uid"`
					} `json:"ownerReferences"`
				} `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
				t.Error(err)
			}
			data, _ := base64.StdEncoding.DecodeString(s.Data["vars.json"])
			if string(data) != `{"join":"join-secret"}` || len(s.Metadata.OwnerReferences) != 1 || s.Metadata.OwnerReferences[0].UID != "job-uid" {
				t.Errorf("invalid private variable secret")
			}
			secret = true
			fmt.Fprint(w, `{}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pods") && strings.HasPrefix(r.URL.Query().Get("labelSelector"), "job-name="):
			fmt.Fprint(w, `{"items":[{"metadata":{"name":"job-pod"},"status":{"phase":"Succeeded","containerStatuses":[{"name":"ansible-runner","state":{"terminated":{"exitCode":0}}}]}}]}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pods/job-pod/log"):
			fmt.Fprint(w, "play completed\n")
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/secrets/"):
			deletedSecret = true
			fmt.Fprint(w, `{}`)
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/jobs/"):
			deletedJob = true
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(404)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	root := t.TempDir()
	token := filepath.Join(root, "token")
	vars := filepath.Join(root, "vars")
	if err := os.WriteFile(token, []byte("service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vars, []byte(`{"join":"join-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	k := KubernetesJob{Endpoint: server.URL, TokenFile: token, HTTP: server.Client(), Namespace: "ansible", PollInterval: time.Millisecond}
	var output bytes.Buffer
	exit, err := k.Run(context.Background(), ExecSpec{Playbook: "playbooks/host-agent.yml", Limit: "node-a", Check: true, ExtraVarsFile: vars}, &output)
	if err != nil || exit != 0 {
		t.Fatalf("job exit=%d error=%v", exit, err)
	}
	if output.String() != "play completed\n" {
		t.Fatalf("output = %q", output.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if !dry || !created || !secret || !deletedJob || !deletedSecret {
		t.Fatalf("job lifecycle dry=%t created=%t secret=%t deletedJob=%t deletedSecret=%t", dry, created, secret, deletedJob, deletedSecret)
	}
}

func TestKubernetesJobRefusesUnexpectedRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("attempted a mutation")
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, `{"spec":{"template":{"spec":{"serviceAccountName":"unexpected"}}}}`)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	job := KubernetesJob{Endpoint: server.URL, TokenFile: token, HTTP: server.Client()}
	if _, err := job.Run(context.Background(), ExecSpec{Playbook: "playbooks/host-agent.yml", Limit: "node-a"}, io.Discard); err == nil {
		t.Fatal("accepted an unexpected runner")
	}
}

func TestKubernetesJobCancellationCleansUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	deleted := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/deployments/ansible-runner"):
			io.WriteString(w, `{"spec":{"template":{"spec":{"serviceAccountName":"ansible-runner","containers":[{"name":"ansible-runner","image":"runner","volumeMounts":[{"name":"ansible-data","mountPath":"/ansible"}]}],"volumes":[{"name":"ansible-data","hostPath":{"path":"/opt/ansible-runner","type":"Directory"}}]}}}}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pods"):
			io.WriteString(w, `{"items":[{"spec":{"nodeName":"node-a"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}}]}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/jobs"):
			io.WriteString(w, `{"metadata":{"uid":"job-uid"}}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/secrets"):
			io.WriteString(w, `{}`)
			cancel()
		case r.Method == "DELETE":
			mu.Lock()
			if strings.Contains(r.URL.Path, "/secrets/") {
				deleted["secret"] = true
			}
			if strings.Contains(r.URL.Path, "/jobs/") {
				deleted["job"] = true
			}
			mu.Unlock()
			io.WriteString(w, `{}`)
		default:
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	vars := filepath.Join(dir, "vars")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vars, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	job := KubernetesJob{Endpoint: server.URL, TokenFile: token, HTTP: server.Client()}
	_, _ = job.Run(ctx, ExecSpec{Playbook: "playbooks/host-agent.yml", Limit: "node-a", ExtraVarsFile: vars}, io.Discard)
	mu.Lock()
	defer mu.Unlock()
	if !deleted["secret"] || !deleted["job"] {
		t.Fatalf("cancel did not clean up: %v", deleted)
	}
}
