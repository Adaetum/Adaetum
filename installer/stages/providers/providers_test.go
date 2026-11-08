package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/provider"
	"github.com/Adaetum/Adaetum/installer/secrets"
	"github.com/Adaetum/Adaetum/installer/sys"
)

func ok(out string) sys.Result { return sys.Result{Stdout: out} }

func TestCloudflareTokenTemplateIncludesDocumentedPermissions(t *testing.T) {
	u, err := url.Parse(cloudflareTokenTemplateURL())
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "dash.cloudflare.com" || u.Query().Get("to") != "/:account/api-tokens" || u.Query().Get("name") != "Adaetum bootstrap" {
		t.Fatalf("unexpected template URL: %s", u)
	}
	var permissions []struct {
		Key  string `json:"key"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(u.Query().Get("permissionGroupKeys")), &permissions); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range permissions {
		got[p.Key+":"+p.Type] = true
	}
	for _, want := range []string{"account_api_tokens:read", "account_api_tokens:edit", "workers_r2:read", "workers_r2:edit", "c07321b023e944ff818fec44d8203567:edit", "workers_scripts:read", "workers_scripts:edit", "zone:read", "dns:read", "dns:edit", "workers_routes:read", "workers_routes:edit"} {
		if !got[want] {
			t.Errorf("template is missing %s", want)
		}
	}
}

// apis starts fake Cloudflare and Tailscale services that accept only the given
// tokens, so a wrong or revoked token is rejected the way the real ones do.
func apis(t *testing.T, cfTokens, tsTokens []string, devices string) provider.Client {
	t.Helper()
	valid := func(auth string, tokens []string) bool {
		for _, tok := range tokens {
			if auth == "Bearer "+tok {
				return true
			}
		}
		return false
	}
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !valid(r.Header.Get("Authorization"), cfTokens) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"success":false}`)
			return
		}
		fmt.Fprint(w, `{"success":true,"result_info":{"total_pages":1},"result":[{"name":"example.com","account":{"id":"acct1","name":"Acme"}}]}`)
	}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !valid(r.Header.Get("Authorization"), tsTokens) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/tailnet/-/keys" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, devices)
	}))
	t.Cleanup(cf.Close)
	t.Cleanup(ts.Close)
	return provider.Client{CloudflareBase: cf.URL, TailscaleBase: ts.URL}
}

const (
	devices = `{"devices":[{"name":"host.tail1234.ts.net."}]}`
	noDev   = `{"devices":[]}`
)

const (
	cfAccess = "python3 ./tasks/scripts/bootstrap-cloudflare.py --token-stdin --account-id acct1 --zone-domain example.com --validate-access-only"
	tsPrep   = "python3 ./tasks/scripts/bootstrap-tailscale.py --user-token-stdin --tailnet tail1234.ts.net --cluster-tag tag:cluster --prepare-policy-only"
	tsMake   = "python3 ./tasks/scripts/bootstrap-tailscale.py --user-token-stdin --tailnet tail1234.ts.net --cluster-tag tag:cluster --create-oauth-client"
	tsOpMake = "python3 ./tasks/scripts/bootstrap-tailscale.py --user-token-stdin --tailnet tail1234.ts.net --create-operator-oauth-client"
)

func runner(extra ...sys.Rule) *sys.Fake {
	base := []sys.Rule{
		{Prefix: "git remote get-url origin", Result: ok("https://github.com/alice/Adaetum-cluster.git\n")},
		{Prefix: "python3 --version", Result: ok("Python 3")},
		{Prefix: "python3 ./tasks/scripts/configure-platform-profile.py --show", Result: ok("domain=adaetum.invalid\noverlay_domain=example-tailnet.ts.net\n")},
		{Prefix: cfAccess, Result: ok("")},
		{Prefix: tsPrep, Result: ok("")},
		{Prefix: tsMake, Result: ok("TAILSCALE_OAUTH_CLIENT_ID=node-id\nTAILSCALE_OAUTH_CLIENT_SECRET=node-secret\n")},
		{Prefix: tsOpMake, Result: ok("TAILSCALE_OPERATOR_OAUTH_CLIENT_ID=op-id\nTAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET=op-secret\n")},
		{Prefix: "python3 ./tasks/scripts/bootstrap-tailscale.py --oauth-credentials-stdin", Result: ok("")},
	}
	return &sys.Fake{Rules: append(extra, base...)}
}

func step(f *sys.Fake, store secrets.Store, api provider.Client) *Step {
	return &Step{Run: f, Dir: "/checkout", Store: store, API: api, Platform: platform.Scripts{Run: f, Dir: "/checkout"}}
}

var fresh = map[string]string{
	"cloudflare-token": "cfat_good", "cloudflare-zone": "example.com",
	"tailscale-token": "tskey-good", "tailscale-tailnet": "tail1234.ts.net",
}

// apply answers the questions the way a headless run does: secrets only through
// their environment variables, never as --answer values.
func apply(t *testing.T, s *Step, env *engine.Env, answers map[string]string) error {
	t.Helper()
	plain, secret := map[string]string{}, map[string]string{}
	for id, v := range answers {
		switch id {
		case "cloudflare-token", "tailscale-token", "cloudflare-replacement-token":
			secret[engine.SecretVar(id)] = v
		default:
			plain[id] = v
		}
	}
	return s.Apply(context.Background(), env, engine.Headless{Answers: plain, Getenv: func(k string) string { return secret[k] }})
}

func TestAFirstRunKeepsProviderCredentialsInMemory(t *testing.T) {
	f, store, env := runner(), &secrets.Memory{}, &engine.Env{}
	s := step(f, store, apis(t, []string{"cfat_good"}, []string{"tskey-good"}, devices))
	if err := apply(t, s, env, fresh); err != nil {
		t.Fatal(err)
	}

	if env.Value("domain") != "example.com" || env.Value("overlay_domain") != "tail1234.ts.net" || env.Value("cloudflare_account_id") != "acct1" {
		t.Errorf("values: %v", env.Values)
	}
	for k, want := range map[string]string{
		"cloudflare_token": "cfat_good", "tailscale_token": "tskey-good",
		"tailscale_oauth_client_id": "node-id", "tailscale_oauth_client_secret": "node-secret",
		"tailscale_operator_oauth_client_id": "op-id", "tailscale_operator_oauth_client_secret": "op-secret",
	} {
		if env.Secret(k) != want {
			t.Errorf("secret %s = %q, want %q", k, env.Secret(k), want)
		}
	}
	ns := "github.com/alice/adaetum-cluster"
	for _, k := range []string{keyCloudflare, keyTailscale, keyOAuthID, keyOAuthSecret, keyOperatorID, keyOperatorSec} {
		if v, err := store.Get(ns, k); v != "" {
			t.Errorf("%s was saved locally (%v)", k, err)
		}
	}
	// Tokens travel on stdin only; no process ever sees one as an argument.
	for _, call := range f.Calls {
		for _, secret := range []string{"cfat_good", "tskey-good", "node-secret", "op-secret"} {
			if strings.Contains(call, secret) {
				t.Errorf("a secret appeared on a command line: %q", call)
			}
		}
	}
	sawStdin := false
	for _, in := range f.Inputs {
		sawStdin = sawStdin || in == "cfat_good" || in == "tskey-good"
	}
	if !sawStdin {
		t.Error("the tokens were never passed on standard input")
	}
}

func TestSavedValidCredentialsAreReusedWithoutAskingForAnything(t *testing.T) {
	f := runner(sys.Rule{Prefix: "python3 ./tasks/scripts/configure-platform-profile.py --show",
		Result: ok("domain=example.com\noverlay_domain=tail1234.ts.net\n")})
	store := &secrets.Memory{}
	ns := "github.com/alice/adaetum-cluster"
	for k, v := range map[string]string{keyCloudflare: "cfat_saved", keyTailscale: "tskey-saved",
		keyOAuthID: "id", keyOAuthSecret: "sec", keyOperatorID: "oid", keyOperatorSec: "osec"} {
		store.Set(ns, k, v)
	}
	s := step(f, store, apis(t, []string{"cfat_saved"}, []string{"tskey-saved"}, devices))
	env := &engine.Env{}
	// No answers at all: any secret prompt would fail with ErrNoAnswer.
	if err := apply(t, s, env, nil); err != nil {
		t.Fatal(err)
	}
	if env.Secret("cloudflare_token") != "cfat_saved" || env.Value("overlay_domain") != "tail1234.ts.net" {
		t.Fatalf("%v %v", env.Secrets, env.Values)
	}
	if f.Ran("python3 ./tasks/scripts/bootstrap-tailscale.py --user-token-stdin --tailnet tail1234.ts.net --cluster-tag tag:cluster --create") {
		t.Error("an OAuth client that was already saved was created again")
	}
}

func TestARejectedSavedTokenIsForgottenAndAskedForAgain(t *testing.T) {
	store := &secrets.Memory{}
	ns := "github.com/alice/adaetum-cluster"
	store.Set(ns, keyCloudflare, "cfat_revoked")
	s := step(runner(), store, apis(t, []string{"cfat_good"}, []string{"tskey-good"}, devices))
	if err := apply(t, s, &engine.Env{}, fresh); err != nil {
		t.Fatal(err)
	}
	if v, _ := store.Get(ns, keyCloudflare); v != "" {
		t.Fatal("the revoked token should be removed without saving the replacement")
	}
}

func TestANonAccountCloudflareTokenNeedsAnExplicitYes(t *testing.T) {
	s := step(runner(), &secrets.Memory{}, apis(t, []string{"legacy-token"}, nil, devices))
	err := apply(t, s, &engine.Env{}, map[string]string{"cloudflare-token": "legacy-token"})
	if !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	// With the explicit yes it is accepted.
	err = apply(t, step(runner(), &secrets.Memory{}, apis(t, []string{"legacy-token"}, []string{"tskey-good"}, devices)), &engine.Env{},
		map[string]string{"cloudflare-token": "legacy-token", "cloudflare-non-account-token": "yes", "cloudflare-zone": "example.com",
			"tailscale-token": "tskey-good", "tailscale-tailnet": "tail1234.ts.net"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMissingCloudflareAccessSendsTheOperatorToFixTheToken(t *testing.T) {
	f := runner(sys.Rule{Prefix: cfAccess, Result: sys.Result{ExitCode: 1}, Then: []sys.Result{{ExitCode: 1}, ok("")}})
	store := &secrets.Memory{}
	s := step(f, store, apis(t, []string{"cfat_good"}, []string{"tskey-good"}, devices))
	if err := apply(t, s, &engine.Env{}, fresh); err != nil {
		t.Fatal(err)
	}
	if v, _ := store.Get("github.com/alice/adaetum-cluster", keyCloudflare); v != "" {
		t.Fatal("the fixed token was saved locally")
	}
}

func TestAReplacementTokenMustCoverTheSameZoneAndAccount(t *testing.T) {
	f := runner(sys.Rule{Prefix: cfAccess, Result: sys.Result{ExitCode: 1}})
	s := step(f, &secrets.Memory{}, apis(t, []string{"cfat_good"}, nil, devices))
	answers := map[string]string{"cloudflare-token": "cfat_good", "cloudflare-zone": "example.com", "cloudflare-replacement-token": "cfat_wrong"}
	if err := apply(t, s, &engine.Env{}, answers); err == nil {
		t.Fatal("a replacement token that cannot read the zone was accepted")
	}
}

func TestANewTailnetAsksForItsNameAndRejectsBadOnes(t *testing.T) {
	api := apis(t, []string{"cfat_good"}, []string{"tskey-good"}, noDev)
	answers := map[string]string{"cloudflare-token": "cfat_good", "cloudflare-zone": "example.com", "tailscale-token": "tskey-good", "tailscale-tailnet": "not-a-tailnet"}
	if err := apply(t, step(runner(), &secrets.Memory{}, api), &engine.Env{}, answers); err == nil {
		t.Fatal("an invalid tailnet name was accepted")
	}
	answers["tailscale-tailnet"] = "https://Tail1234.TS.net/"
	env := &engine.Env{}
	if err := apply(t, step(runner(), &secrets.Memory{}, api), env, answers); err != nil {
		t.Fatal(err)
	}
	if env.Value("overlay_domain") != "tail1234.ts.net" {
		t.Fatalf("overlay domain = %q", env.Value("overlay_domain"))
	}
}

func TestNothingIsSavedWhenThereIsNoCredentialStore(t *testing.T) {
	store := &secrets.Memory{Off: true}
	s := step(runner(), store, apis(t, []string{"cfat_good"}, []string{"tskey-good"}, devices))
	// No "save-*" answers either: none may be asked.
	if err := apply(t, s, &engine.Env{}, fresh); err != nil {
		t.Fatal(err)
	}
	if v, _ := store.Get("github.com/alice/adaetum-cluster", keyCloudflare); v != "" {
		t.Fatal("a credential was saved with no credential store")
	}
}

func TestADeclinedSaveStillUsesTheTokenButDoesNotKeepIt(t *testing.T) {
	store := &secrets.Memory{}
	s := step(runner(), store, apis(t, []string{"cfat_good"}, []string{"tskey-good"}, devices))
	answers := map[string]string{}
	for k, v := range fresh {
		answers[k] = v
	}
	answers["save-cloudflare-setup-token"] = "no"
	env := &engine.Env{}
	if err := apply(t, s, env, answers); err != nil {
		t.Fatal(err)
	}
	if v, _ := store.Get("github.com/alice/adaetum-cluster", keyCloudflare); v != "" {
		t.Fatal("a credential was saved after the operator said no")
	}
	if env.Secret("cloudflare_token") != "cfat_good" {
		t.Fatal("the token was not kept in memory for later stages")
	}
}

func TestCleanIgnoresSavedCredentials(t *testing.T) {
	store := &secrets.Memory{}
	store.Set("github.com/alice/adaetum-cluster", keyCloudflare, "cfat_saved")
	s := step(runner(), store, apis(t, []string{"cfat_good", "cfat_saved"}, []string{"tskey-good"}, devices))
	env := &engine.Env{Clean: true}
	if err := apply(t, s, env, fresh); err != nil {
		t.Fatal(err)
	}
	if env.Secret("cloudflare_token") != "cfat_good" {
		t.Fatalf("a clean run reused the saved token: %q", env.Secret("cloudflare_token"))
	}
}

func TestSeveralZonesNeedAnExplicitChoice(t *testing.T) {
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"result_info":{"total_pages":1},"result":[{"name":"a.com","account":{"id":"1","name":"A"}},{"name":"b.com","account":{"id":"2","name":"B"}}]}`)
	}))
	t.Cleanup(cf.Close)
	s := step(runner(), &secrets.Memory{}, provider.Client{CloudflareBase: cf.URL})
	// No zone answer: a silent run must not guess which domain to use.
	err := apply(t, s, &engine.Env{}, map[string]string{"cloudflare-token": "cfat_x"})
	if !errors.Is(err, engine.ErrNoAnswer) {
		t.Fatalf("err = %v", err)
	}
}

// retryUI is a front end that can show a rejected answer and ask again, like the
// window, and records every screen it was asked.
type retryUI struct {
	engine.Headless
	tokens  []string // successive answers to the cloudflare-token screen
	screens []engine.Screen
}

func (r *retryUI) CanRetry() bool { return true }
func (r *retryUI) Ask(_ context.Context, s engine.Screen) (string, error) {
	r.screens = append(r.screens, s)
	switch {
	case s.ID == "cloudflare-token":
		v := r.tokens[0]
		r.tokens = r.tokens[1:]
		return v, nil
	case s.ID == "cloudflare-zone":
		return "example.com", nil
	case s.ID == "tailscale-token":
		return "tskey-good", nil
	case s.ID == "tailscale-tailnet":
		return "tail1234.ts.net", nil
	case s.Kind == engine.Confirm:
		return s.Default, nil
	}
	return s.Default, nil
}

func TestABadTokenIsExplainedOnTheSameScreenAndCanBeReplacedThere(t *testing.T) {
	ui := &retryUI{tokens: []string{"cfat_wrong", "cfat_good"}}
	s := step(runner(), &secrets.Memory{}, apis(t, []string{"cfat_good"}, []string{"tskey-good"}, devices))
	env := &engine.Env{}
	if err := s.Apply(context.Background(), env, ui); err != nil {
		t.Fatal(err)
	}
	var asks []engine.Screen
	for _, sc := range ui.screens {
		if sc.ID == "cloudflare-token" {
			asks = append(asks, sc)
		}
	}
	if len(asks) != 2 {
		t.Fatalf("the token was asked %d times, want 2", len(asks))
	}
	if asks[0].Error != "" || asks[1].Error == "" {
		t.Fatalf("the rejection was not shown on the second ask: first=%q second=%q", asks[0].Error, asks[1].Error)
	}
	if env.Secret("cloudflare_token") != "cfat_good" {
		t.Fatalf("token = %q", env.Secret("cloudflare_token"))
	}
}

func TestInstructionsAndTheTokenBoxAreOneScreen(t *testing.T) {
	ui := &retryUI{tokens: []string{"cfat_good"}}
	s := step(runner(), &secrets.Memory{}, apis(t, []string{"cfat_good"}, []string{"tskey-good"}, devices))
	if err := s.Apply(context.Background(), &engine.Env{}, ui); err != nil {
		t.Fatal(err)
	}
	for _, sc := range ui.screens {
		if sc.ID == "cloudflare-token" {
			hasLink := false
			for _, st := range sc.Steps {
				hasLink = hasLink || strings.HasPrefix(st.Link, "https://dash.cloudflare.com")
			}
			if sc.Kind != engine.Secret || sc.Title == "" || len(sc.Steps) < 3 || !hasLink {
				t.Fatalf("the token screen is not a guided, structured screen: %+v", sc)
			}
			if len(sc.Prompt) > 40 {
				t.Fatalf("the prompt should be a short label, not a paragraph: %q", sc.Prompt)
			}
		}
		if sc.ID == "cloudflare-token-ready" {
			t.Fatal("there is still a separate 'I made the token' screen")
		}
	}
}
