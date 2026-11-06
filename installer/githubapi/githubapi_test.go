package githubapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/nacl/box"
)

func client(t *testing.T, h http.HandlerFunc) Client {
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return Client{Token: "tok-secret", Base: s.URL, Sleep: func(time.Duration) {}}
}

func TestASecretIsEncryptedToTheRepositoryKeyAndNeverSentInTheClear(t *testing.T) {
	pub, priv, _ := box.GenerateKey(rand.Reader)
	var gotBody map[string]string
	var gotPath, auth string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprintf(w, `{"key":%q,"key_id":"kid1"}`, base64.StdEncoding.EncodeToString(pub[:]))
		case http.MethodPut:
			gotPath = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), "the-secret-value") {
				t.Error("the secret was sent in the clear")
			}
			json.Unmarshal(raw, &gotBody)
			w.WriteHeader(http.StatusCreated)
		}
	})
	if err := c.SetSecret(context.Background(), "alice/repo", "Prod", "R2_BUCKET", "the-secret-value"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/alice/repo/environments/Prod/secrets/R2_BUCKET" || gotBody["key_id"] != "kid1" || auth != "Bearer tok-secret" {
		t.Fatalf("path=%q body=%v auth=%q", gotPath, gotBody, auth)
	}
	// Only the holder of the private key can read it back.
	sealed, _ := base64.StdEncoding.DecodeString(gotBody["encrypted_value"])
	plain, ok := box.OpenAnonymous(nil, sealed, pub, priv)
	if !ok || string(plain) != "the-secret-value" {
		t.Fatalf("the sealed value does not decrypt to the secret: ok=%v %q", ok, plain)
	}
}

func TestAMissingEnvironmentIsReportedSoTheCallerCanFallBack(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	err := c.SetSecret(context.Background(), "a/b", "Prod", "X", "v")
	if !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestTransientFailuresAreRetriedAndPermanentOnesAreNot(t *testing.T) {
	var calls int32
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"full_name":"a/b","permissions":{"push":true}}`)
	})
	r, err := c.Repo(context.Background(), "a/b")
	if err != nil || !r.Permissions.Push || calls != 3 {
		t.Fatalf("repo=%+v err=%v calls=%d", r, err, calls)
	}

	calls = 0
	c = client(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := c.Repo(context.Background(), "a/b"); err == nil || calls != 1 {
		t.Fatalf("a 401 was retried (%d calls) or ignored: %v", calls, err)
	}
}

func TestTheTokenNeverAppearsInAnError(t *testing.T) {
	c := Client{Token: "tok-secret", Base: "http://127.0.0.1:1", Sleep: func(time.Duration) {}}
	_, err := c.Repo(context.Background(), "a/b")
	if err == nil || strings.Contains(err.Error(), "tok-secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestDispatchAndRunLookup(t *testing.T) {
	var dispatched map[string]any
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dispatches"):
			json.NewDecoder(r.Body).Decode(&dispatched)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/runs") && r.URL.Query().Get("branch") == "main":
			fmt.Fprint(w, `{"workflow_runs":[{"id":11},{"id":10}]}`)
		case strings.HasSuffix(r.URL.Path, "/actions/runs/11"):
			fmt.Fprint(w, `{"status":"completed","conclusion":"success","html_url":"https://example/run/11"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	})
	if err := c.Dispatch(context.Background(), "a/b", "iso-build.yml", "main", map[string]string{"ks_template_file_name": "x.ks"}); err != nil {
		t.Fatal(err)
	}
	if dispatched["ref"] != "main" {
		t.Fatalf("%v", dispatched)
	}
	ids, err := c.RunIDs(context.Background(), "a/b", "iso-build.yml", "main")
	if err != nil || len(ids) != 2 || ids[0] != 11 {
		t.Fatalf("%v %v", ids, err)
	}
	run, err := c.Run(context.Background(), "a/b", 11)
	if err != nil || run.Conclusion != "success" {
		t.Fatalf("%+v %v", run, err)
	}
}
