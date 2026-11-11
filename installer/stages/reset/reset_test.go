package reset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const envText = `TAILSCALE_DOMAIN=example.ts.net
TAILSCALE_OAUTH_CLIENT_ID=node-client
TAILSCALE_OPERATOR_OAUTH_CLIENT_ID=op-client
TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET=op-secret
CLOUDFLARE_API_TOKEN=cf-token
CLOUDFLARE_ACCOUNT_ID=acct
R2_BUCKET=iso
`

const (
	devicesList = `{"devices":[
		{"id":"1","hostname":"node-42187","name":"node-42187.example.ts.net","tags":["tag:rocky10","tag:server","tag:cluster"],"lastSeen":"2026-11-11T02:09:00Z","isHost":true},
		{"id":"2","hostname":"adaetum-ingress-0","tags":["tag:k8s"],"isHost":false}],"deleted":[]}`
	devicesGone = `{"devices":[],"deleted":["1","2"]}`
	clientList  = `{"clients":[{"id":"node-client","description":"Adaetum node enrollment"},{"id":"op-client","description":"Adaetum Kubernetes operator"}],"deleted":[]}`
	clientGone  = `{"clients":[],"deleted":["node-client","op-client"]}`
	logFound    = `{"found":{"node-42187":38},"deleted":{}}`
	logGone     = `{"found":{},"deleted":{"node-42187":38}}`
)

func ok(out string) sys.Result { return sys.Result{Stdout: out} }

func harness(t *testing.T, env string) (*Step, *sys.Fake) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	const ts = "python3 ./tasks/scripts/bootstrap-tailscale.py"
	const cf = "python3 ./tasks/scripts/bootstrap-cloudflare.py"
	f := &sys.Fake{Rules: []sys.Rule{
		// Rules with a confirmation come first: the listing call is a prefix of them.
		{Prefix: ts + " --oauth-credentials-stdin --tailnet example.ts.net --cluster-tag tag:cluster --reset-devices --reset-confirm-ids 1,2", Result: ok(devicesGone)},
		{Prefix: ts + " --oauth-credentials-stdin --tailnet example.ts.net --cluster-tag tag:cluster --reset-devices --reset-confirm-ids 1", Result: ok(`{"devices":[],"deleted":["1"]}`)},
		{Prefix: ts + " --oauth-credentials-stdin --tailnet example.ts.net --cluster-tag tag:cluster --reset-devices", Result: ok(devicesList)},
		{Prefix: ts + " --user-token-stdin --tailnet example.ts.net --reset-oauth-clients --reset-client-ids node-client,op-client --reset-confirm-ids node-client,op-client", Result: ok(clientGone)},
		{Prefix: ts + " --user-token-stdin --tailnet example.ts.net --reset-oauth-clients", Result: ok(clientList)},
		{Prefix: cf + " --token-stdin --account-id acct --bucket iso --delete-host-logs node-42187 --delete-host-logs-confirm", Result: ok(logGone)},
		{Prefix: cf + " --token-stdin --account-id acct --bucket iso --delete-host-logs node-42187", Result: ok(logFound)},
	}}
	return New(f, dir, ""), f
}

func run(t *testing.T, s *Step, answers map[string]string, token string) error {
	t.Helper()
	secrets := map[string]string{}
	if token != "" {
		secrets[engine.SecretVar("reset-tailscale-token")] = token
	}
	return s.Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: answers, Getenv: func(k string) string { return secrets[k] }})
}

func anyCall(f *sys.Fake, sub string) bool {
	for _, c := range f.Calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func TestAHeadlessRunDeletesNothingUnlessToldTo(t *testing.T) {
	s, f := harness(t, envText)
	// No reset-what answer: the default choice is "Nothing, just look".
	if err := run(t, s, nil, "ts-token"); !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("err = %v, want the run to stop at the menu", err)
	}
	for _, forbidden := range []string{"--reset-confirm-ids", "--delete-host-logs-confirm", "--reset-oauth-clients"} {
		if anyCall(f, forbidden) {
			t.Errorf("a look-only run ran a delete: %s", forbidden)
		}
	}
	if !anyCall(f, "--reset-devices") || !anyCall(f, "--delete-host-logs node-42187") {
		t.Errorf("the reset did not list what it would remove: %v", f.Calls)
	}
}

func TestChoosingEverythingStillNeedsTheFinalYes(t *testing.T) {
	s, f := harness(t, envText)
	err := run(t, s, map[string]string{"reset-what": "everything"}, "ts-token") // no reset-confirm
	if !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("err = %v, want the final confirmation to decline by default", err)
	}
	if anyCall(f, "--reset-confirm-ids") || anyCall(f, "--delete-host-logs-confirm") {
		t.Error("deleted without the final yes")
	}
}

func TestConfirmingRemovesExactlyWhatWasListed(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--reset-devices --reset-confirm-ids 1,2",
		"--delete-host-logs node-42187 --delete-host-logs-confirm",
		"--reset-oauth-clients --reset-client-ids node-client,op-client --reset-confirm-ids node-client,op-client",
	} {
		if !anyCall(f, want) {
			t.Errorf("missing call %q in %v", want, f.Calls)
		}
	}
	// Only the host's logs are cleaned, never the operator's proxy device.
	if anyCall(f, "adaetum-ingress-0") {
		t.Error("tried to clean logs for a proxy device")
	}
}

func TestDevicesAndLogsOnlyKeepsTheOAuthClientsAndNeverAsksForAToken(t *testing.T) {
	s, f := harness(t, envText)
	// No token in the environment: asking for one would fail the run.
	if err := run(t, s, map[string]string{"reset-what": "devices and logs", "reset-confirm": "yes"}, ""); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-oauth-clients") {
		t.Error("touched the OAuth clients when told to keep them")
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1,2") {
		t.Error("devices should have been removed")
	}
}

func TestPickingDevicesOneByOneRemovesOnlyThoseAndTheirHostLogs(t *testing.T) {
	s, f := harness(t, envText)
	answers := map[string]string{"reset-what": "choose", "reset-device-1": "yes", "reset-device-2": "no", "reset-clients": "no", "reset-confirm": "yes"}
	if err := run(t, s, answers, ""); err != nil {
		t.Fatal(err)
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1") || anyCall(f, "--reset-confirm-ids 1,2") {
		t.Errorf("wrong devices removed: %v", f.Calls)
	}
	if !anyCall(f, "--delete-host-logs node-42187 --delete-host-logs-confirm") {
		t.Error("the removed host's logs should have been cleaned")
	}
	if anyCall(f, "--reset-oauth-clients") {
		t.Error("removed OAuth clients that were declined")
	}
}

func TestOAuthClientsOnlyLeavesDevicesAlone(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "oauth", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-devices --reset-confirm-ids") || anyCall(f, "--delete-host-logs-confirm") {
		t.Error("removed devices or logs when only the OAuth clients were chosen")
	}
	if !anyCall(f, "--reset-oauth-clients --reset-client-ids node-client,op-client --reset-confirm-ids") {
		t.Error("the OAuth clients were not removed")
	}
}

func TestSecretsReachTheToolsOnStandardInputOnly(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls {
		for _, secret := range []string{"op-secret", "cf-token", "ts-token"} {
			if strings.Contains(c, secret) {
				t.Errorf("a secret appeared in an argument list: %s", c)
			}
		}
	}
	joined := strings.Join(f.Inputs, "|")
	for _, want := range []string{"op-client\nop-secret\n", "cf-token", "ts-token"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q on standard input", want)
		}
	}
}

func TestWithoutATokenTheOAuthClientsAreKept(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, ""); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-oauth-clients") {
		t.Error("touched OAuth clients without the access token that is needed to remove them")
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1,2") {
		t.Error("devices should still be removed without the token")
	}
}

func TestAnEnvWithoutTheOperatorClientIsBlocked(t *testing.T) {
	s, _ := harness(t, "TAILSCALE_DOMAIN=example.ts.net\n")
	finding, err := s.Check(context.Background(), &engine.Env{})
	if err != nil || finding.Status != engine.Blocked {
		t.Fatalf("finding = %+v, err = %v", finding, err)
	}
}
