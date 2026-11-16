package about

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Source reads about files out of the cluster repository.
type Source interface {
	// Paths lists every about file in the repository.
	Paths(ctx context.Context) ([]string, error)
	// File returns one file's content.
	File(ctx context.Context, path string) ([]byte, error)
}

// Gitea reads the cluster repository over Gitea's API with the credential the agent
// already holds for proposing updates. It only ever reads.
type Gitea struct {
	BaseURL, Owner, Repository, Branch, TokenFile string
	HTTP                                          *http.Client
}

// NewGitea points at the in-cluster Gitea.
func NewGitea(owner, repository, branch, tokenFile string) *Gitea {
	return &Gitea{
		BaseURL: "http://gitea-http.gitea.svc.cluster.local:3000", Owner: owner, Repository: repository, Branch: branch, TokenFile: tokenFile,
		HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (g *Gitea) get(ctx context.Context, path string, limit int64) ([]byte, error) {
	token, err := os.ReadFile(g.TokenFile)
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return nil, errors.New("the repository credential is not available")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+strings.TrimSpace(string(token)))
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Gitea request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gitea answered HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (g *Gitea) repo() string {
	return "/api/v1/repos/" + url.PathEscape(g.Owner) + "/" + url.PathEscape(g.Repository)
}

// Paths walks the repository tree, a page at a time, keeping only about files.
func (g *Gitea) Paths(ctx context.Context) ([]string, error) {
	var found []string
	for page := 1; page <= 20; page++ {
		data, err := g.get(ctx, fmt.Sprintf("%s/git/trees/%s?recursive=true&per_page=1000&page=%d", g.repo(), url.PathEscape(g.Branch), page), 8<<20)
		if err != nil {
			return nil, err
		}
		var tree struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"tree"`
			Truncated bool `json:"truncated"`
		}
		if err := json.Unmarshal(data, &tree); err != nil {
			return nil, errors.New("the repository listing was unreadable")
		}
		for _, entry := range tree.Tree {
			if _, ok := Name(entry.Path); ok && entry.Type == "blob" {
				found = append(found, entry.Path)
			}
		}
		if !tree.Truncated {
			return found, nil
		}
	}
	return found, nil
}

// File reads one file on the branch.
func (g *Gitea) File(ctx context.Context, path string) ([]byte, error) {
	escaped := make([]string, 0, 4)
	for _, part := range strings.Split(path, "/") {
		escaped = append(escaped, url.PathEscape(part))
	}
	return g.get(ctx, g.repo()+"/raw/"+strings.Join(escaped, "/")+"?ref="+url.QueryEscape(g.Branch), maxFileBytes+1)
}

// Result is the answer for one app.
type Result struct {
	// About is nil when the repository has no description for the app.
	About *Document `json:"about"`
	// Problem is set when a description exists but cannot be shown.
	Problem string `json:"problem,omitempty"`
}

// API serves GET /api/v1/about/{app}. The index of about files is read at most once every
// few minutes, since descriptions change only when someone merges a change.
type API struct {
	Source      Source
	RequireUser func(http.Handler) http.Handler
	// Now is the clock for the cache; tests replace it.
	Now func() time.Time

	mu      sync.Mutex
	paths   map[string]string
	fetched time.Time
	failed  error
}

const cacheFor = 5 * time.Minute

// Register adds the route behind the sign-in guard.
func (api *API) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(api.serve)
	if api.RequireUser != nil {
		handler = api.RequireUser(handler)
	}
	mux.Handle("GET /api/v1/about/{app}", handler)
}

func (api *API) index(ctx context.Context) (map[string]string, error) {
	now := time.Now
	if api.Now != nil {
		now = api.Now
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.paths != nil && now().Sub(api.fetched) < cacheFor {
		return api.paths, nil
	}
	paths, err := api.Source.Paths(ctx)
	if err != nil {
		// Keep serving the last good index rather than blanking every card.
		if api.paths != nil {
			return api.paths, nil
		}
		return nil, err
	}
	index := map[string]string{}
	for _, path := range paths {
		if name, ok := Name(path); ok {
			// Two files for one app name: the shorter path wins so the answer is stable.
			if existing, taken := index[name]; !taken || path < existing {
				index[name] = path
			}
		}
	}
	api.paths, api.fetched = index, now()
	return index, nil
}

func (api *API) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	result := Result{}
	if api.Source == nil {
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	index, err := api.index(r.Context())
	if err != nil {
		// A repository that cannot be read is "no description yet", not a broken page.
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	path, ok := index[r.PathValue("app")]
	if !ok {
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	data, err := api.Source.File(r.Context(), path)
	if err != nil {
		result.Problem = "The description exists but could not be read from the repository."
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	doc, err := Parse(data)
	if err != nil {
		result.Problem = "The description in the repository is not valid: " + err.Error()
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	result.About = &doc
	_ = json.NewEncoder(w).Encode(result)
}
