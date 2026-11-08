// Package githubapi is the installer's own client for the parts of GitHub's REST
// API that publishing needs: environments, encrypted secrets, workflow dispatch and
// run status. It replaces manage-github-secrets.py and a set of curl one-liners.
//
// The token is sent only as a request header to GitHub's own address. Nothing here
// puts a token or a secret value on a command line, and error text never contains
// either.
package githubapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/box"
)

const apiBase = "https://api.github.com"

// Client talks to GitHub. The zero Base is the real API; tests point it elsewhere.
type Client struct {
	Token string
	Base  string
	HTTP  *http.Client
	// Sleep waits between retries; tests replace it.
	Sleep func(time.Duration)
}

// Error is a non-success answer from GitHub.
type Error struct {
	Status int
	Body   string
}

func (e *Error) Error() string { return fmt.Sprintf("GitHub returned HTTP %d: %s", e.Status, e.Body) }

func (c Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return apiBase
}

func (c Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c Client) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

func retryable(status int) bool { return status == 429 || (status >= 500 && status < 600) }

// do sends one request, retrying transient failures (rate limits, 5xx, network
// errors) with a doubling delay, or Retry-After when GitHub gives one.
func (c Client) do(ctx context.Context, method, path string, body any, attempts int) (int, []byte, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		payload = b
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base()+path, bytes.NewReader(payload))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "adaetum-installer")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.client().Do(req)
		if err != nil {
			lastErr = errors.New("network error: " + strings.ReplaceAll(err.Error(), c.Token, "[token]"))
			if attempt < attempts {
				c.sleep(time.Duration(1<<(attempt-1)) * time.Second)
				continue
			}
			return 0, nil, lastErr
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode < 300 {
			return resp.StatusCode, data, nil
		}
		lastErr = &Error{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
		if retryable(resp.StatusCode) && attempt < attempts {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			if ra, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && ra > 0 {
				delay = time.Duration(ra) * time.Second
			}
			c.sleep(delay)
			continue
		}
		return resp.StatusCode, data, lastErr
	}
	return 0, nil, lastErr
}

// ---- environments and secrets -------------------------------------------------

// EnsureEnvironment creates the deployment environment if it is missing.
func (c Client) EnsureEnvironment(ctx context.Context, repo, env string) error {
	_, _, err := c.do(ctx, http.MethodPut, "/repos/"+repo+"/environments/"+url.PathEscape(env), map[string]any{}, 4)
	return err
}

// ErrEnvironmentUnavailable means environment-scoped secrets cannot be used here
// (the repository plan or permissions do not allow it), so repository-level
// secrets are the fallback.
var ErrEnvironmentUnavailable = errors.New("environment secrets are not available for this repository")

// SetSecret stores an encrypted secret, in the environment when env is set and at
// repository level otherwise. The value is encrypted on this machine with the
// repository's public key (a libsodium sealed box), so GitHub never receives it in
// the clear and it never appears in any argument list.
func (c Client) SetSecret(ctx context.Context, repo, env, name, value string) error {
	keyPath := "/repos/" + repo + "/actions/secrets/public-key"
	putPath := "/repos/" + repo + "/actions/secrets/" + url.PathEscape(name)
	if env != "" {
		e := url.PathEscape(env)
		keyPath = "/repos/" + repo + "/environments/" + e + "/secrets/public-key"
		putPath = "/repos/" + repo + "/environments/" + e + "/secrets/" + url.PathEscape(name)
	}
	status, data, err := c.do(ctx, http.MethodGet, keyPath, nil, 4)
	if err != nil {
		if env != "" && (status == http.StatusNotFound || status == http.StatusForbidden) {
			return ErrEnvironmentUnavailable
		}
		return err
	}
	var key struct {
		Key   string `json:"key"`
		KeyID string `json:"key_id"`
	}
	if err := json.Unmarshal(data, &key); err != nil || key.Key == "" || key.KeyID == "" {
		return errors.New("GitHub returned an invalid public key")
	}
	sealed, err := Seal(key.Key, value)
	if err != nil {
		return err
	}
	_, _, err = c.do(ctx, http.MethodPut, putPath, map[string]string{"encrypted_value": sealed, "key_id": key.KeyID}, 4)
	return err
}

// Seal encrypts value to a GitHub public key (base64) as a sealed box.
func Seal(publicKeyB64, value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil || len(raw) != 32 {
		return "", errors.New("GitHub returned a public key that is not 32 bytes")
	}
	var pub [32]byte
	copy(pub[:], raw)
	out, err := box.SealAnonymous(nil, []byte(value), &pub, rand.Reader)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// ---- repositories -------------------------------------------------------------

// Repo is the slice of repository metadata setup reads.
type Repo struct {
	FullName    string `json:"full_name"`
	Permissions struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

func (c Client) Repo(ctx context.Context, repo string) (Repo, error) {
	_, data, err := c.do(ctx, http.MethodGet, "/repos/"+repo, nil, 3)
	if err != nil {
		return Repo{}, err
	}
	var r Repo
	return r, json.Unmarshal(data, &r)
}

// ---- workflows ----------------------------------------------------------------

// Dispatch triggers a workflow_dispatch run. These workflows are declarative and
// safe to repeat, so a transient provider failure is retried for longer than
// other calls.
func (c Client) Dispatch(ctx context.Context, repo, workflowFile, ref string, inputs map[string]string) error {
	if inputs == nil {
		inputs = map[string]string{}
	}
	_, _, err := c.do(ctx, http.MethodPost, "/repos/"+repo+"/actions/workflows/"+url.PathEscape(workflowFile)+"/dispatches",
		map[string]any{"ref": ref, "inputs": inputs}, 6)
	return err
}

// RunIDs lists the ids of recent workflow_dispatch runs of a workflow on a branch.
func (c Client) RunIDs(ctx context.Context, repo, workflowFile, branch string) ([]int64, error) {
	path := "/repos/" + repo + "/actions/workflows/" + url.PathEscape(workflowFile) + "/runs?event=workflow_dispatch&per_page=30&branch=" + url.QueryEscape(branch)
	_, data, err := c.do(ctx, http.MethodGet, path, nil, 3)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Runs []struct {
			ID int64 `json:"id"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(doc.Runs))
	for _, r := range doc.Runs {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// Run is a workflow run's state.
type Run struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

func (c Client) Run(ctx context.Context, repo string, id int64) (Run, error) {
	_, data, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/actions/runs/%d", repo, id), nil, 3)
	if err != nil {
		return Run{}, err
	}
	var r Run
	return r, json.Unmarshal(data, &r)
}
