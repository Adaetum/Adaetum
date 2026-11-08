package releases

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const originalKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
images:
  - name: adaetum-agent
    newName: ghcr.io/adaetum/adaetum-agent
    newTag: old
  - name: adaetum-auth
    newName: ghcr.io/adaetum/adaetum-auth
    newTag: old
  - name: adaetum-embed
    newName: ghcr.io/adaetum/adaetum-embed
    newTag: old
  - name: unrelated
    newTag: keep
`

func TestPinImagesOnlyChangesKnownEntries(t *testing.T) {
	result, err := PinImages([]byte(originalKustomization), fixture("1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Images []struct {
			Name    string `yaml:"name"`
			NewName string `yaml:"newName"`
			NewTag  string `yaml:"newTag"`
			Digest  string `yaml:"digest"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(result, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Images) != 4 || doc.Images[0].Digest != "sha256:"+strings.Repeat("b", 64) || doc.Images[0].NewTag != "" || doc.Images[3].Name != "unrelated" || doc.Images[3].NewTag != "keep" {
		t.Fatalf("unexpected image rewrite: %s", result)
	}
	if _, err := PinImages(result, fixture("1.2.3")); err == nil {
		t.Fatal("already pinned release was accepted")
	}
	if _, err := PinImages([]byte(strings.Replace(originalKustomization, "name: adaetum-auth", "name: adaetum-agent", 1)), fixture("1.2.3")); err == nil {
		t.Fatal("duplicate image was accepted")
	}
}
func TestGitOpsCreatesASeparateReviewBranchAndPullRequest(t *testing.T) {
	var server *httptest.Server
	var sawCommit, sawPR bool
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token test-token" {
			t.Error("missing token")
		}
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/contents/"+gitopsFile):
			fmt.Fprintf(w, `{"sha":"abc","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte(originalKustomization)))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/contents/adaetum/host-agent-release.json"):
			fmt.Fprint(w, `{"sha":"host-sha"}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/contents"):
			var body struct {
				Branch    string `json:"branch"`
				NewBranch string `json:"new_branch"`
				Files     []struct{ Path, Operation, Content, SHA string }
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			decoded, _ := base64.StdEncoding.DecodeString(body.Files[0].Content)
			if len(body.Files) == 2 {
				data, err := base64.StdEncoding.DecodeString(body.Files[1].Content)
				var lock struct {
					Version string            `json:"version"`
					SHA256  map[string]string `json:"sha256"`
				}
				if err != nil || json.Unmarshal(data, &lock) != nil || lock.Version != "1.2.3" || lock.SHA256["amd64"] != strings.Repeat("c", 64) || lock.SHA256["arm64"] != strings.Repeat("c", 64) {
					t.Errorf("invalid host release pin: %s", data)
				}
			}
			if len(body.Files) != 2 || body.Files[1].Path != "adaetum/host-agent-release.json" || body.Files[1].SHA != "host-sha" {
				t.Errorf("missing host release pin: %+v", body.Files)
			}
			if body.Branch != "main" || !strings.HasPrefix(body.NewBranch, "adaetum/update-v1.2.3-") || body.Files[0].Path != gitopsFile || body.Files[0].SHA != "abc" || !strings.Contains(string(decoded), "digest: sha256:") {
				t.Errorf("unexpected change: %+v", body)
			}
			sawCommit = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/pulls"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["base"] != "main" || !strings.HasPrefix(body["head"], "adaetum/update-v1.2.3-") {
				t.Errorf("unexpected pull: %v", body)
			}
			sawPR = true
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number":1}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	g := GitOps{BaseURL: server.URL, Owner: "owner", Repository: "repo", Branch: "main", TokenFile: token, HTTP: server.Client()}
	proposal, err := g.CreateProposal(context.Background(), fixture("1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	if !sawCommit || !sawPR || !strings.HasSuffix(proposal.URL, "/pulls/1") {
		t.Fatalf("proposal=%+v commit=%v pull=%v", proposal, sawCommit, sawPR)
	}
}
