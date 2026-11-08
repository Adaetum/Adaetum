// Package accounts is the Go agent's side of people signing in with their own accounts.
//
// The accounts themselves live in a separate auth service (auth/ in this repository: Better
// Auth, maintained upstream, so password and session security is not code we write). This
// package only talks to it, and decides who may do what:
//
//   - Client:  a small HTTP client for the auth service's local socket.
//   - Guard:   lets a request through if it carries the access token OR a valid session.
//     The access token is the emergency login and never depends on the auth service, so
//     a crashed or missing auth service cannot lock an administrator out.
//   - Proxy:   passes the sign-in pages' calls to the auth service, and nothing else.
//   - Sync:    keeps this machine's copy of the account list current (see docs/recovery-login.md).
//
// The agent never reads the auth service's database file and the auth service never
// reads the agent's: two processes, two files, one owner each.
package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// InternalHeader carries the shared secret to the auth service's internal routes.
const InternalHeader = "X-Adaetum-Internal"

// maxReply bounds what is read back from the auth service. A snapshot is a few rows per
// person, so this is far above any real account list.
const maxReply = 8 << 20

// Client talks to one auth service.
type Client struct {
	http   *http.Client
	base   string
	secret string
}

// NewUnixClient connects to the auth service's socket.
func NewUnixClient(socket, secret string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 15 * time.Second}, base: "http://auth", secret: secret}
}

// NewClient points a Client at any base URL (used by tests, and for development over a port).
func NewClient(base, secret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{http: httpClient, base: strings.TrimRight(base, "/"), secret: secret}
}

// State is how far this machine's account list has got.
type State struct {
	Mode    string `json:"mode"`
	Schema  int    `json:"schema"`
	Epoch   string `json:"epoch"`
	Version int64  `json:"version"`
	Users   int    `json:"users"`
	Owners  int    `json:"owners"`
}

// ApplyResult is the auth service's verdict on a copy of the accounts.
type ApplyResult struct {
	Status  string `json:"status"` // applied, current or rejected
	Version int64  `json:"version,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Person is someone who can sign in. Passwords are never part of this.
type Person struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// AuthState is what the sign-in page needs to know before anyone is signed in.
type AuthState struct {
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode,omitempty"`
	NeedsSetup  bool   `json:"needsSetup,omitempty"`
	HasAccounts bool   `json:"hasAccounts,omitempty"`
	// Unavailable means the auth service did not answer: sign-in with accounts is down,
	// and only the access token will work.
	Unavailable bool `json:"unavailable,omitempty"`
}

// StatusError is a non-success answer, with what the service said.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("the auth service answered %d: %s", e.Code, e.Body)
}

// Unavailable reports whether err means the auth service could not be reached at all
// (as opposed to it answering "no").
func Unavailable(err error) bool {
	var status *StatusError
	return err != nil && !errors.As(err, &status)
}

func (c *Client) do(ctx context.Context, method, path string, headers map[string]string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		switch typed := body.(type) {
		case json.RawMessage:
			reader = bytes.NewReader(typed)
		default:
			data, err := json.Marshal(body)
			if err != nil {
				return err
			}
			reader = bytes.NewReader(data)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.HasPrefix(path, "/internal/") {
		request.Header.Set(InternalHeader, c.secret)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxReply+1))
	if err != nil {
		return err
	}
	if len(data) > maxReply {
		return errors.New("the auth service's answer is larger than any real account list")
	}
	if response.StatusCode/100 != 2 {
		return &StatusError{Code: response.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	return json.Unmarshal(data, out)
}

// AuthState asks the public state route, as a browser would.
func (c *Client) AuthState(ctx context.Context) (AuthState, error) {
	var state AuthState
	err := c.do(ctx, http.MethodGet, "/api/auth-state", nil, nil, &state)
	return state, err
}

// State is this machine's account-list position.
func (c *Client) State(ctx context.Context) (State, error) {
	var state State
	err := c.do(ctx, http.MethodGet, "/internal/state", nil, nil, &state)
	return state, err
}

// Snapshot returns this machine's accounts exactly as the auth service wrote them. It is
// kept as raw JSON on purpose: the agent only carries it, so it cannot reinterpret it.
// ErrNobody means there is nobody to copy yet.
var ErrNobody = errors.New("there are no accounts to copy")

func (c *Client) Snapshot(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.do(ctx, http.MethodGet, "/internal/snapshot", nil, nil, &raw)
	var status *StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return nil, ErrNobody
	}
	return raw, err
}

// Apply hands a copy to the auth service, which accepts or refuses it by its own rules.
// A refusal is returned as a result (Status "rejected"), not an error.
func (c *Client) Apply(ctx context.Context, snapshot json.RawMessage) (ApplyResult, error) {
	var result ApplyResult
	err := c.do(ctx, http.MethodPost, "/internal/snapshot", nil, snapshot, &result)
	var status *StatusError
	if errors.As(err, &status) && status.Code == http.StatusUnprocessableEntity {
		if json.Unmarshal([]byte(status.Body), &result) == nil && result.Status == "rejected" {
			return result, nil
		}
	}
	return result, err
}

// Replace hands a copy to the auth service with the cluster-wins rule: a replica drops its
// own accounts for the cluster's, whatever their history or version. The service still
// refuses a copy that is empty, ownerless or in another layout, and a primary refuses all.
func (c *Client) Replace(ctx context.Context, snapshot json.RawMessage) (ApplyResult, error) {
	var result ApplyResult
	err := c.do(ctx, http.MethodPost, "/internal/snapshot?takeover=1", nil, snapshot, &result)
	var status *StatusError
	if errors.As(err, &status) && status.Code == http.StatusUnprocessableEntity {
		if json.Unmarshal([]byte(status.Body), &result) == nil && result.Status == "rejected" {
			return result, nil
		}
	}
	return result, err
}

// SetMode says whether this machine owns its accounts ("primary") or copies them ("replica").
func (c *Client) SetMode(ctx context.Context, mode string) error {
	return c.do(ctx, http.MethodPost, "/internal/mode", nil, map[string]string{"mode": mode}, nil)
}

// People lists everyone who can sign in here.
func (c *Client) People(ctx context.Context) (mode string, people []Person, err error) {
	var reply struct {
		Mode   string   `json:"mode"`
		People []Person `json:"people"`
	}
	err = c.do(ctx, http.MethodGet, "/internal/people", nil, nil, &reply)
	return reply.Mode, reply.People, err
}

// NewPerson is the form for adding someone.
type NewPerson struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (c *Client) AddPerson(ctx context.Context, person NewPerson) (Person, error) {
	var created Person
	err := c.do(ctx, http.MethodPost, "/internal/people", nil, person, &created)
	return created, err
}

func (c *Client) RemovePerson(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/internal/people/"+url.PathEscape(id), nil, nil, nil)
}

func (c *Client) SetRole(ctx context.Context, id, role string) error {
	return c.do(ctx, http.MethodPost, "/internal/people/"+url.PathEscape(id)+"/role", nil, map[string]string{"role": role}, nil)
}

func (c *Client) RevokeSessions(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/internal/people/"+url.PathEscape(id)+"/revoke-sessions", nil, nil, nil)
}

// Identity is who a request comes from.
type Identity struct {
	ID    string `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	Role  string `json:"role"`
	// Token is true when the access token was used rather than an account.
	Token bool `json:"viaToken,omitempty"`
}

// ErrNoSession means the cookie does not belong to a signed-in person.
var ErrNoSession = errors.New("not signed in")

// Session resolves a browser's Cookie header to a person.
func (c *Client) Session(ctx context.Context, cookie string) (Identity, error) {
	var reply *struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Name  string `json:"name"`
			Role  string `json:"role"`
		} `json:"user"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/auth/get-session", map[string]string{"Cookie": cookie}, nil, &reply); err != nil {
		return Identity{}, err
	}
	if reply == nil || reply.User.ID == "" {
		return Identity{}, ErrNoSession
	}
	return Identity{ID: reply.User.ID, Email: reply.User.Email, Name: reply.User.Name, Role: reply.User.Role}, nil
}
