// Package notify is the Notifications section of Settings: where Adaetum's alerts go.
//
// THE PIPELINE ALREADY EXISTS. Alertmanager sends warnings and criticals to Apprise, and
// Apprise delivers them to every URL in its configuration, one of which is the cluster's own
// ntfy (topic adaetum-alerts) that bootstrap seeds. What was missing is a way for the operator
// to say where else they go, without editing an OpenBao field by hand.
//
// SOURCE OF TRUTH. Apprise's whole configuration is one field of one OpenBao secret
// (secret/apps/observability/apprise, field apprise_yml). This package reads and edits that
// field and nothing else; Apprise follows the mounted file within a couple of minutes. A
// destination URL usually contains a token, so URLs are never sent to the browser: the page
// sees a masked label and an id.
//
// WHAT PROTECTS A CHANGE. Letting the agent write this secret means a compromised agent
// could redirect or silence alerts, so every change is recorded BEFORE it is made (and not
// made if the record cannot be written), a new destination must pass a real test message
// first, the built-in ntfy destination cannot be removed, and the write is version-checked so
// it never overwrites a newer edit. Everything else in the secret, and the operator's
// comments and extra entries in the YAML, are kept as they were.
package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	secretPath = "apps/observability/apprise"
	field      = "apprise_yml"
	// builtinHost is the in-cluster ntfy the platform seeds. It carries the cluster's own alerts
	// to the operator's phone, so the page never removes it.
	builtinHost = "ntfy.observability.svc.cluster.local"
	maxURL      = 2000
)

var scheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://\S+$`)

// Secret is the part of OpenBao this package needs.
type Secret interface {
	ReadFields(ctx context.Context, path string) (map[string]any, int, bool, error)
	WriteFields(ctx context.Context, path string, fields map[string]any, expectedVersion int) error
}

// Destination is one place alerts are delivered, as the page sees it.
type Destination struct {
	// ID names the destination to remove it; it is a hash of the URL, so it reveals nothing.
	ID string `json:"id"`
	// Label is the URL with everything secret masked.
	Label string `json:"label"`
	// BuiltIn marks the cluster's own ntfy, which cannot be removed.
	BuiltIn bool `json:"builtIn,omitempty"`
	// Custom marks an entry the page did not write and cannot edit (it is kept as it is).
	Custom bool `json:"custom,omitempty"`
}

// TestResult is the outcome of the last test message.
type TestResult struct {
	At      time.Time `json:"at"`
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
}

// State is the page's whole view.
type State struct {
	Destinations []Destination `json:"destinations"`
	// Editable is false when OpenBao cannot be read or written; Reason says why.
	Editable bool        `json:"editable"`
	Reason   string      `json:"reason,omitempty"`
	LastTest *TestResult `json:"lastTest,omitempty"`
}

// API serves /api/v1/notifications.
type API struct {
	Secret Secret
	// AppriseURL is the in-cluster Apprise API, such as http://apprise.observability.svc.cluster.local:8000.
	AppriseURL string
	// Audit records a change BEFORE it is made; if it fails, nothing is changed.
	Audit       func(entry any) error
	RequireUser func(http.Handler) http.Handler
	Who         func(*http.Request) string
	HTTP        *http.Client
	Now         func() time.Time

	mu       sync.Mutex // one change at a time
	lastTest *TestResult
}

// Register adds the routes behind the sign-in guard.
func (a *API) Register(mux *http.ServeMux) {
	guard := a.RequireUser
	if guard == nil {
		guard = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /api/v1/notifications", guard(http.HandlerFunc(a.get)))
	mux.Handle("POST /api/v1/notifications/test", guard(http.HandlerFunc(a.test)))
	mux.Handle("POST /api/v1/notifications/destinations", guard(http.HandlerFunc(a.add)))
	mux.Handle("DELETE /api/v1/notifications/destinations/{id}", guard(http.HandlerFunc(a.remove)))
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *API) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 25 * time.Second}
}

// load reads Apprise's configuration and the secret's version.
func (a *API) load(ctx context.Context) (fields map[string]any, version int, config string, err error) {
	if a.Secret == nil {
		return nil, 0, "", errors.New("the agent is not set up to reach OpenBao")
	}
	fields, version, found, err := a.Secret.ReadFields(ctx, secretPath)
	if err != nil {
		return nil, 0, "", err
	}
	if !found {
		return nil, 0, "", errors.New("OpenBao holds no alert configuration yet; platform setup has not finished")
	}
	config, _ = fields[field].(string)
	return fields, version, config, nil
}

func (a *API) state(ctx context.Context) State {
	state := State{Destinations: []Destination{}}
	a.mu.Lock()
	state.LastTest = a.lastTest
	a.mu.Unlock()
	_, _, config, err := a.load(ctx)
	if err != nil {
		state.Reason = "Destinations cannot be read right now: " + err.Error() + "."
		return state
	}
	list, err := destinations(config)
	if err != nil {
		state.Reason = "The alert configuration is not something this page can read, so it is left alone."
		return state
	}
	state.Destinations, state.Editable = list, true
	return state
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.state(r.Context()))
}

// test sends one message through the saved configuration, so it shows what an alert would do.
func (a *API) test(w http.ResponseWriter, r *http.Request) {
	ok, message := a.send(r.Context(), "/notify/apprise", map[string]string{
		"title": "Adaetum test notification",
		"body":  "If you can read this, alerts from your cluster will reach you.",
		"type":  "info",
	}, "")
	a.mu.Lock()
	a.lastTest = &TestResult{At: a.now().UTC(), OK: ok, Message: message}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, a.state(r.Context()))
}

// send posts to Apprise and says in plain words how it went. secret is scrubbed from any text
// returned, since Apprise may echo a URL back.
func (a *API) send(ctx context.Context, path string, body map[string]string, secret string) (bool, string) {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.AppriseURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return false, "Apprise's address is not valid."
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client().Do(req)
	if err != nil {
		return false, "Apprise could not be reached, so nothing was sent."
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusOK {
		return true, "Sent. Check the places listed above."
	}
	detail := strings.TrimSpace(string(raw))
	if secret != "" {
		detail = strings.ReplaceAll(detail, secret, "[destination]")
	}
	if len(detail) > 200 {
		detail = detail[:200]
	}
	return false, fmt.Sprintf("Apprise could not deliver it (HTTP %d). %s", resp.StatusCode, detail)
}

type addRequest struct {
	URL string `json:"url"`
}

func (a *API) add(w http.ResponseWriter, r *http.Request) {
	var body addRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "unreadable request")
		return
	}
	target := strings.TrimSpace(body.URL)
	if len(target) > maxURL || !scheme.MatchString(target) {
		fail(w, http.StatusBadRequest, "That is not an Apprise address. It looks like ntfy://host/topic or mailto://user:password@example.com (see the Apprise list of services).")
		return
	}
	// A destination must really deliver before it is saved: this catches a typo in a token now,
	// instead of during an outage.
	if ok, message := a.send(r.Context(), "/notify", map[string]string{
		"urls": target, "title": "Adaetum: new alert destination", "body": "This destination was just added to your cluster's alerts.", "type": "info",
	}, target); !ok {
		fail(w, http.StatusBadGateway, "Not saved. The test message did not go through. "+message)
		return
	}
	a.change(w, r, "add", labelFor(target), func(root *yaml.Node) error {
		seq := urlsNode(root, true)
		for _, item := range seq.Content {
			if item.Value == target {
				return errors.New("That destination is already in the list.")
			}
		}
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: target})
		return nil
	})
}

func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.change(w, r, "remove", id, func(root *yaml.Node) error {
		seq := urlsNode(root, false)
		if seq == nil {
			return errNotFound
		}
		for i, item := range seq.Content {
			if item.Kind != yaml.ScalarNode || idFor(item.Value) != id {
				continue
			}
			if isBuiltIn(item.Value) {
				return errors.New("That is the cluster's own ntfy, the way alerts reach your phone. It stays.")
			}
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			return nil
		}
		return errNotFound
	})
}

var errNotFound = errors.New("That destination is no longer in the list.")

// change applies one edit to the configuration: record it, edit the YAML, store it only if
// nobody else changed it meanwhile.
func (a *API) change(w http.ResponseWriter, r *http.Request, action, what string, edit func(*yaml.Node) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fields, version, config, err := a.load(r.Context())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "Nothing was changed. "+err.Error()+".")
		return
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(config), &root); err != nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		fail(w, http.StatusConflict, "The alert configuration is not something this page can edit, so it was left alone.")
		return
	}
	if err := edit(&root); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	// The record comes first: a change nobody can later see is not made.
	if a.Audit == nil {
		fail(w, http.StatusServiceUnavailable, "Nothing was changed because there is no record to write it to.")
		return
	}
	who := ""
	if a.Who != nil {
		who = a.Who(r)
	}
	if err := a.Audit(map[string]any{"time": a.now().UTC(), "setting": "alert-destination", "action": action, "destination": what, "by": who}); err != nil {
		fail(w, http.StatusServiceUnavailable, "Nothing was changed because the record cannot be written.")
		return
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		fail(w, http.StatusInternalServerError, "The new configuration could not be written.")
		return
	}
	_ = encoder.Close()
	fields[field] = out.String()
	if err := a.Secret.WriteFields(r.Context(), secretPath, fields, version); err != nil {
		fail(w, http.StatusBadGateway, "Nothing was changed: "+err.Error()+".")
		return
	}
	state := State{Destinations: []Destination{}, LastTest: a.lastTest}
	if list, err := destinations(out.String()); err == nil {
		state.Destinations, state.Editable = list, true
	}
	writeJSON(w, http.StatusOK, state)
}

// urlsNode finds the urls list in an Apprise configuration, creating it when asked.
func urlsNode(root *yaml.Node, create bool) *yaml.Node {
	mapping := root.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "urls" {
			return mapping.Content[i+1]
		}
	}
	if !create {
		return nil
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "urls"}, seq)
	return seq
}

// destinations lists what an Apprise configuration delivers to.
func destinations(config string) ([]Destination, error) {
	list := []Destination{}
	if strings.TrimSpace(config) == "" {
		return list, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(config), &root); err != nil {
		return nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping")
	}
	seq := urlsNode(&root, false)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return list, nil
	}
	for _, item := range seq.Content {
		if item.Kind != yaml.ScalarNode {
			// A tagged or detailed entry written by hand: kept, shown, never edited here.
			list = append(list, Destination{ID: idFor(fmt.Sprint(item.Line)), Label: "A custom entry (edited in OpenBao)", Custom: true})
			continue
		}
		list = append(list, Destination{ID: idFor(item.Value), Label: labelFor(item.Value), BuiltIn: isBuiltIn(item.Value)})
	}
	return list, nil
}

func idFor(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func isBuiltIn(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "ntfy" && parsed.Hostname() == builtinHost
}

// labelFor masks everything in an Apprise URL that could be a secret. Tokens often sit where a
// host would (tgram://TOKEN/chat, discord://id/token), so a host is shown only when it looks
// like one: it has a dot or is localhost.
func labelFor(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		// Not parseable (a token with a colon where a port would be): show only the service.
		if at := strings.Index(value, "://"); at > 0 {
			return value[:at] + "://•••"
		}
		return "A destination"
	}
	label := parsed.Scheme + "://"
	if parsed.User != nil {
		// A user name can be a key (pover://userkey@token), so it is masked like the password.
		label += "•••@"
	}
	host := parsed.Hostname()
	if strings.Contains(host, ".") || host == "localhost" {
		label += host
		if port := parsed.Port(); port != "" {
			label += ":" + port
		}
	} else {
		label += "•••"
	}
	return label
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
