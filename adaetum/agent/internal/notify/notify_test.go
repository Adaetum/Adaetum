package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const seed = `# alerts go here
version: 1
urls:
  - "ntfy://apprise:sekret@ntfy.observability.svc.cluster.local:80/adaetum-alerts"
`

type fakeSecret struct {
	fields    map[string]any
	version   int
	writeErr  error
	readErr   error
	lastWrite map[string]any
}

func (f *fakeSecret) ReadFields(context.Context, string) (map[string]any, int, bool, error) {
	if f.readErr != nil {
		return nil, 0, false, f.readErr
	}
	copied := map[string]any{}
	for k, v := range f.fields {
		copied[k] = v
	}
	return copied, f.version, true, nil
}

func (f *fakeSecret) WriteFields(_ context.Context, _ string, fields map[string]any, expected int) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	if expected != f.version {
		return errors.New("version moved")
	}
	f.fields, f.lastWrite = fields, fields
	f.version++
	return nil
}

// apprise is a stand-in for the Apprise API: it accepts everything unless the url holds "bad".
func apprise(t *testing.T, sent *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		*sent = append(*sent, r.URL.Path+" "+body["urls"])
		if strings.Contains(body["urls"], "bad") {
			http.Error(w, "could not deliver to "+body["urls"], http.StatusFailedDependency)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func newAPI(t *testing.T, secret *fakeSecret, server *httptest.Server, audits *[]any, auditErr error) (*API, *http.ServeMux) {
	api := &API{Secret: secret, AppriseURL: server.URL, HTTP: server.Client(), Audit: func(entry any) error {
		if auditErr != nil {
			return auditErr
		}
		*audits = append(*audits, entry)
		return nil
	}}
	mux := http.NewServeMux()
	api.Register(mux)
	return api, mux
}

func call(mux *http.ServeMux, method, path, body string) (int, string) {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.String()
}

func TestListMasksSecretsAndProtectsTheBuiltIn(t *testing.T) {
	var sent []string
	server := apprise(t, &sent)
	defer server.Close()
	_, mux := newAPI(t, &fakeSecret{fields: map[string]any{field: seed}, version: 1}, server, new([]any), nil)
	code, body := call(mux, "GET", "/api/v1/notifications", "")
	if code != 200 || strings.Contains(body, "sekret") || !strings.Contains(body, `"builtIn":true`) || !strings.Contains(body, `"editable":true`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestAddNeedsAPassingTestAndKeepsEverythingElse(t *testing.T) {
	var sent []string
	server := apprise(t, &sent)
	defer server.Close()
	secret := &fakeSecret{fields: map[string]any{field: seed, "other": "kept"}, version: 3}
	var audits []any
	_, mux := newAPI(t, secret, server, &audits, nil)

	// A destination that fails its test is not saved, and the failure does not echo the secret.
	code, body := call(mux, "POST", "/api/v1/notifications/destinations", `{"url":"json://user:bad@example.com/x"}`)
	if code != http.StatusBadGateway || strings.Contains(body, "user:bad") || secret.version != 3 || len(audits) != 0 {
		t.Fatalf("a failing test must not save: %d %s", code, body)
	}
	if code, _ := call(mux, "POST", "/api/v1/notifications/destinations", `{"url":"not a url"}`); code != http.StatusBadRequest {
		t.Fatalf("a malformed address must be refused, got %d", code)
	}

	code, body = call(mux, "POST", "/api/v1/notifications/destinations", `{"url":"tgram://123456:token/99"}`)
	if code != 200 || secret.version != 4 || len(audits) != 1 {
		t.Fatalf("%d %s (version %d, audits %d)", code, body, secret.version, len(audits))
	}
	if strings.Contains(body, "123456") || strings.Contains(body, "token") {
		t.Fatalf("the response leaked the secret: %s", body)
	}
	saved, _ := secret.fields[field].(string)
	if !strings.Contains(saved, "# alerts go here") || !strings.Contains(saved, "tgram://123456:token/99") || !strings.Contains(saved, "sekret") || secret.fields["other"] != "kept" {
		t.Fatalf("the edit must keep comments, the built-in entry and other fields:\n%s", saved)
	}
	if code, _ := call(mux, "POST", "/api/v1/notifications/destinations", `{"url":"tgram://123456:token/99"}`); code != http.StatusConflict {
		t.Fatalf("a duplicate must be refused, got %d", code)
	}
}

func TestRemoveRefusesTheBuiltInAndRecordsFirst(t *testing.T) {
	var sent []string
	server := apprise(t, &sent)
	defer server.Close()
	config := seed + `  - "tgram://123456:token/99"` + "\n"
	secret := &fakeSecret{fields: map[string]any{field: config}, version: 1}
	var audits []any
	_, mux := newAPI(t, secret, server, &audits, nil)

	builtIn := idFor("ntfy://apprise:sekret@ntfy.observability.svc.cluster.local:80/adaetum-alerts")
	if code, _ := call(mux, "DELETE", "/api/v1/notifications/destinations/"+builtIn, ""); code != http.StatusConflict || secret.version != 1 {
		t.Fatalf("the built-in destination must stay, got %d", code)
	}
	if code, _ := call(mux, "DELETE", "/api/v1/notifications/destinations/"+idFor("tgram://123456:token/99"), ""); code != 200 || secret.version != 2 || len(audits) != 1 {
		t.Fatalf("removal failed: %d version %d audits %d", code, secret.version, len(audits))
	}
	if strings.Contains(secret.fields[field].(string), "tgram") {
		t.Fatal("the destination is still stored")
	}
}

func TestNothingChangesWithoutARecordOrWhenOpenBaoRefuses(t *testing.T) {
	var sent []string
	server := apprise(t, &sent)
	defer server.Close()
	secret := &fakeSecret{fields: map[string]any{field: seed}, version: 1}
	_, mux := newAPI(t, secret, server, new([]any), errors.New("disk full"))
	if code, _ := call(mux, "POST", "/api/v1/notifications/destinations", `{"url":"tgram://1:t/2"}`); code != http.StatusServiceUnavailable || secret.version != 1 {
		t.Fatalf("a change with no record must not happen, got %d", code)
	}
	down := &fakeSecret{readErr: errors.New("OpenBao is sealed")}
	_, mux = newAPI(t, down, server, new([]any), nil)
	code, body := call(mux, "GET", "/api/v1/notifications", "")
	if code != 200 || !strings.Contains(body, `"editable":false`) || !strings.Contains(body, "sealed") {
		t.Fatalf("an unreadable vault must be explained, not an error: %d %s", code, body)
	}
}

func TestTestSendsThroughTheSavedConfigAndRemembersTheOutcome(t *testing.T) {
	var sent []string
	server := apprise(t, &sent)
	defer server.Close()
	_, mux := newAPI(t, &fakeSecret{fields: map[string]any{field: seed}, version: 1}, server, new([]any), nil)
	code, body := call(mux, "POST", "/api/v1/notifications/test", "")
	if code != 200 || !strings.Contains(body, `"ok":true`) || len(sent) != 1 || !strings.HasPrefix(sent[0], "/notify/apprise") {
		t.Fatalf("%d %s %v", code, body, sent)
	}
}

func TestLabelMasksTokensWhereAHostWouldBe(t *testing.T) {
	for url, want := range map[string]string{
		"tgram://123456:AAAtoken/99":              "tgram://•••",
		"discord://12345/webhooktoken":            "discord://•••",
		"mailto://me:pw@gmail.com?to=you@x.com":   "mailto://•••@gmail.com",
		"pover://userkey@apptoken":                "pover://•••@•••",
		"ntfy://u:p@ntfy.example.com:8443/alerts": "ntfy://•••@ntfy.example.com:8443",
	} {
		if got := labelFor(url); got != want {
			t.Errorf("%s: got %q want %q", url, got, want)
		}
	}
}
