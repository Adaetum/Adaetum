package releases

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const gitopsFile = "pods/adaetum/agent/kustomization.yaml"

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var branchName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

type Proposal struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
}

// GitOps proposes an update to exactly one known file on a new branch in the
// operator's existing Gitea repository. It never merges or changes the base branch.
type GitOps struct {
	BaseURL, PublicHost, Owner, Repository, Branch, TokenFile string
	HTTP                                          *http.Client
}

func (g GitOps) configured() error {
	if !repoPart.MatchString(g.Owner) || !repoPart.MatchString(g.Repository) || !branchName.MatchString(g.Branch) || strings.Contains(g.Branch, "..") || strings.HasSuffix(g.Branch, "/") {
		return errors.New("the GitOps repository or branch is not configured")
	}
	if g.TokenFile == "" {
		return errors.New("the GitOps update credential is not configured")
	}
	if g.PublicHost != "" && !regexp.MustCompile(`^[A-Za-z0-9.-]+$`).MatchString(g.PublicHost) { return errors.New("the public Gitea host is invalid") }
	return nil
}
func (g GitOps) call(ctx context.Context, method, path string, body any, out any) error {
	token, err := os.ReadFile(g.TokenFile)
	if err != nil {
		return errors.New("the GitOps update credential is not available")
	}
	value := strings.TrimSpace(string(token))
	if value == "" {
		return errors.New("the GitOps update credential is empty")
	}
	var payload io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return e
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(g.BaseURL, "/")+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+value)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Gitea request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Gitea %s returned HTTP %d", method, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
			return fmt.Errorf("decode Gitea response: %w", err)
		}
	}
	return nil
}
func mappingGet(node *yaml.Node, key string) (*yaml.Node, int) {
	if node.Kind != yaml.MappingNode {
		return nil, -1
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1], i
		}
	}
	return nil, -1
}
func mappingSet(node *yaml.Node, key, value string) {
	old, i := mappingGet(node, key)
	if old != nil {
		old.Kind = yaml.ScalarNode
		old.Tag = "!!str"
		old.Value = value
		return
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	_ = i
}
func mappingDelete(node *yaml.Node, key string) {
	_, i := mappingGet(node, key)
	if i >= 0 {
		node.Content = append(node.Content[:i], node.Content[i+2:]...)
	}
}

// PinImages changes only the three known Kustomize image entries, preserving the
// rest of the operator's file and rejecting missing or duplicate entries.
func PinImages(content []byte, m Manifest) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("read agent kustomization: %w", err)
	}
	if len(doc.Content) != 1 {
		return nil, errors.New("agent kustomization is empty")
	}
	images, _ := mappingGet(doc.Content[0], "images")
	if images == nil || images.Kind != yaml.SequenceNode {
		return nil, errors.New("agent kustomization has no image list")
	}
	seen := map[string]bool{}
	changed := false
	for _, item := range images.Content {
		name, _ := mappingGet(item, "name")
		if name == nil {
			continue
		}
		component := strings.TrimPrefix(name.Value, "adaetum-")
		if component != "agent" && component != "auth" && component != "embed" {
			continue
		}
		if name.Value != "adaetum-"+component || seen[component] {
			return nil, errors.New("agent kustomization has duplicate image entries")
		}
		seen[component] = true
		image, ok := m.Images[component]
		if !ok {
			return nil, fmt.Errorf("release is missing the %s image", component)
		}
		target, digest, ok := strings.Cut(image, "@sha256:")
		if !ok || target != "ghcr.io/adaetum/adaetum-"+component || !hashPattern.MatchString(digest) {
			return nil, fmt.Errorf("release has an invalid %s digest", component)
		}
		oldName, _ := mappingGet(item, "newName")
		oldDigest, _ := mappingGet(item, "digest")
		oldTag, _ := mappingGet(item, "newTag")
		if oldName == nil || oldName.Value != target || oldDigest == nil || oldDigest.Value != "sha256:"+digest || oldTag != nil {
			changed = true
		}
		mappingSet(item, "newName", target)
		mappingDelete(item, "newTag")
		mappingSet(item, "digest", "sha256:"+digest)
	}
	for _, c := range []string{"agent", "auth", "embed"} {
		if !seen[c] {
			return nil, fmt.Errorf("agent kustomization is missing the %s image", c)
		}
	}
	if !changed {
		return nil, errors.New("this release is already pinned")
	}
	var output bytes.Buffer
	enc := yaml.NewEncoder(&output)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func (g GitOps) CreateProposal(ctx context.Context, m Manifest) (Proposal, error) {
	if err := g.configured(); err != nil {
		return Proposal{}, err
	}
	prefix := "/api/v1/repos/" + url.PathEscape(g.Owner) + "/" + url.PathEscape(g.Repository)
	var current struct {
		SHA      string `json:"sha"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := g.call(ctx, "GET", prefix+"/contents/"+gitopsFile+"?ref="+url.QueryEscape(g.Branch), nil, &current); err != nil {
		return Proposal{}, err
	}
	if current.SHA == "" || current.Encoding != "base64" {
		return Proposal{}, errors.New("Gitea returned invalid file metadata")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(current.Content, "\n", ""))
	if err != nil {
		return Proposal{}, errors.New("Gitea returned invalid file content")
	}
	updated, err := PinImages(decoded, m)
	if err != nil {
		return Proposal{}, err
	}
	if bytes.Equal(bytes.TrimSpace(decoded), bytes.TrimSpace(updated)) {
		return Proposal{}, errors.New("this release is already pinned")
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Proposal{}, err
	}
	branch := "adaetum/update-v" + m.Version + "-" + hex.EncodeToString(random[:])
	payload := map[string]any{"branch": g.Branch, "new_branch": branch, "message": "Update Adaetum agent to " + m.Version, "files": []any{map[string]any{"operation": "update", "path": gitopsFile, "sha": current.SHA, "content": base64.StdEncoding.EncodeToString(updated)}}}
	if err := g.call(ctx, "POST", prefix+"/contents", payload, nil); err != nil {
		return Proposal{}, err
	}
	var pull struct { Number int `json:"number"` }
	body := map[string]any{"base": g.Branch, "head": branch, "title": "Update Adaetum agent to " + m.Version, "body": "Pins the agent, auth, and embedding images to the immutable digests in the Adaetum release manifest. Review and merge to apply the update."}
	if err := g.call(ctx, "POST", prefix+"/pulls", body, &pull); err != nil {
		return Proposal{Branch: branch}, fmt.Errorf("the update branch %s was created, but the pull request failed: %w", branch, err)
	}
	if pull.Number <= 0 { return Proposal{Branch: branch}, errors.New("the pull request was created but Gitea returned no pull request number") }
	base := strings.TrimRight(g.BaseURL, "/")
	if g.PublicHost != "" {
		if !regexp.MustCompile(`^[A-Za-z0-9.-]+$`).MatchString(g.PublicHost) { return Proposal{Branch: branch}, errors.New("the pull request was created but the public Gitea host is invalid") }
		base = "https://" + g.PublicHost
	}
	return Proposal{URL: fmt.Sprintf("%s/%s/%s/pulls/%d", base, url.PathEscape(g.Owner), url.PathEscape(g.Repository), pull.Number), Branch: branch}, nil
}

func NewGitOps(owner, repo, branch, publicHost, tokenFile string) GitOps {
	return GitOps{BaseURL: "http://gitea-http.gitea.svc.cluster.local:3000", PublicHost: publicHost, Owner: owner, Repository: repo, Branch: branch, TokenFile: tokenFile, HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
